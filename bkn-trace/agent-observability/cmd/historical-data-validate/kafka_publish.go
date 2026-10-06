// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/conf"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/kafkaaccess/auditconsumer"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/kafkaaccess/auditvalidator"
	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl"
	"github.com/segmentio/kafka-go/sasl/plain"
	"github.com/segmentio/kafka-go/sasl/scram"
)

type kafkaPublisher struct {
	writer     *kafka.Writer
	completion chan kafkaCompletion
	transport  *kafka.Transport
}
type kafkaCompletion struct {
	messages []kafka.Message
	err      error
}

func newKafkaPublisher() (auditPublisher, error) {
	username := os.Getenv("BKN_HISTORY_KAFKA_USERNAME")
	password := os.Getenv("BKN_HISTORY_KAFKA_PASSWORD")
	mechanismName := os.Getenv("BKN_HISTORY_KAFKA_MECHANISM")
	var brokers []string
	for _, entry := range strings.Split(os.Getenv("BKN_HISTORY_KAFKA_BROKERS"), ",") {
		if host := strings.TrimSpace(entry); host != "" {
			brokers = append(brokers, host)
		}
	}
	if len(brokers) == 0 || username == "" || password == "" || mechanismName == "" {
		return nil, errors.New("authenticated Kafka environment configuration required")
	}
	for _, address := range brokers {
		if !qualificationAddress(address) {
			return nil, errors.New("qualification Kafka must use loopback brokers")
		}
	}
	var mechanism sasl.Mechanism
	var err error
	switch mechanismName {
	case "PLAIN":
		mechanism = plain.Mechanism{Username: username, Password: password}
	case "SCRAM-SHA-256":
		mechanism, err = scram.Mechanism(scram.SHA256, username, password)
	case "SCRAM-SHA-512":
		mechanism, err = scram.Mechanism(scram.SHA512, username, password)
	default:
		return nil, errors.New("unsupported Kafka SASL mechanism")
	}
	if err != nil {
		return nil, errors.New("kafka SASL initialization failed")
	}
	transport := &kafka.Transport{SASL: mechanism, DialTimeout: 10 * time.Second, Resolver: qualificationResolver{}, Dial: qualificationDial}
	return publisherWithTransport(transport, brokers)
}

func inPlaceAuditConfig() (conf.KafkaTopicConsumerConfig, error) {
	// Use the deployed producer principal, never the independent READ-only
	// consumer credentials. No upgrade-specific credentials are introduced.
	config := conf.KafkaTopicConsumerConfig{Topic: auditconsumer.Topic,
		Enabled:       os.Getenv("BKN_AUDIT_KAFKA_ENABLED") == "true",
		SASLMechanism: strings.TrimSpace(os.Getenv("BKN_AUDIT_KAFKA_SASL_MECHANISM")),
		Username:      strings.TrimSpace(os.Getenv("BKN_AUDIT_KAFKA_USERNAME")),
		Password:      os.Getenv("BKN_AUDIT_KAFKA_PASSWORD")}
	for _, entry := range strings.Split(os.Getenv("BKN_AUDIT_KAFKA_BROKERS"), ",") {
		if broker := strings.TrimSpace(entry); broker != "" {
			config.Brokers = append(config.Brokers, broker)
		}
	}
	if !config.Enabled || len(config.Brokers) == 0 || config.SASLMechanism == "" || config.Username == "" || config.Password == "" {
		return conf.KafkaTopicConsumerConfig{}, errors.New("enabled native Audit publisher configuration required")
	}
	return config, nil
}

func newInPlaceKafkaPublisher() (auditPublisher, error) {
	configuration, err := inPlaceAuditConfig()
	if err != nil {
		return nil, err
	}
	var mechanism sasl.Mechanism
	switch configuration.SASLMechanism {
	case "PLAIN":
		mechanism = plain.Mechanism{Username: configuration.Username, Password: configuration.Password}
	case "SCRAM-SHA-256":
		mechanism, err = scram.Mechanism(scram.SHA256, configuration.Username, configuration.Password)
	case "SCRAM-SHA-512":
		mechanism, err = scram.Mechanism(scram.SHA512, configuration.Username, configuration.Password)
	default:
		return nil, errors.New("unsupported native Kafka authentication")
	}
	if err != nil {
		return nil, errors.New("native Kafka authentication initialization failed")
	}
	transport := &kafka.Transport{SASL: mechanism, DialTimeout: 10 * time.Second}
	return publisherWithTransport(transport, configuration.Brokers)
}

func publisherWithTransport(transport *kafka.Transport, brokers []string) (auditPublisher, error) {
	if err := verifyQualificationTimestampPolicy(transport, brokers); err != nil {
		transport.CloseIdleConnections()
		return nil, errors.New("native Audit topic LogAppendTime verification failed")
	}
	publisher := &kafkaPublisher{completion: make(chan kafkaCompletion, 1), transport: transport}
	publisher.writer = &kafka.Writer{
		Addr: kafka.TCP(brokers...), Topic: auditconsumer.Topic, Balancer: &kafka.Hash{}, RequiredAcks: kafka.RequireAll, MaxAttempts: 1,
		BatchSize: 1, ReadTimeout: 20 * time.Second, WriteTimeout: 20 * time.Second, Transport: transport,
		Completion: func(messages []kafka.Message, err error) {
			publisher.completion <- kafkaCompletion{messages: messages, err: err}
		},
	}
	return publisher, nil
}

func qualificationAddress(address string) bool {
	host, port, err := net.SplitHostPort(address)
	if err != nil || (host != "localhost" && host != "127.0.0.1" && host != "::1") {
		return false
	}
	number, err := strconv.Atoi(port)
	return err == nil && number > 0 && number <= 65535
}

// Both resolver and dial guard advertised broker redirects. localhost is
// resolved to a fixed loopback IP rather than trusting host DNS configuration.
type qualificationResolver struct{}

func (qualificationResolver) LookupBrokerIPAddr(_ context.Context, broker kafka.Broker) ([]net.IPAddr, error) {
	if !qualificationAddress(net.JoinHostPort(broker.Host, strconv.Itoa(broker.Port))) {
		return nil, errors.New("external advertised Kafka broker refused")
	}
	host := broker.Host
	if host == "localhost" {
		host = "127.0.0.1"
	}
	return []net.IPAddr{{IP: net.ParseIP(host)}}, nil
}
func qualificationDial(ctx context.Context, network, address string) (net.Conn, error) {
	if !qualificationAddress(address) || (network != "tcp" && network != "tcp4" && network != "tcp6") {
		return nil, errors.New("external Kafka connection refused")
	}
	host, port, _ := net.SplitHostPort(address)
	if host == "localhost" {
		address = net.JoinHostPort("127.0.0.1", port)
	}
	return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, address)
}
func verifyQualificationTimestampPolicy(transport *kafka.Transport, brokers []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client := &kafka.Client{Transport: transport}
	response, err := client.DescribeConfigs(ctx, &kafka.DescribeConfigsRequest{Addr: kafka.TCP(brokers...), Resources: []kafka.DescribeConfigRequestResource{{ResourceType: kafka.ResourceTypeTopic, ResourceName: auditconsumer.Topic, ConfigNames: []string{"message.timestamp.type"}}}})
	if err != nil || len(response.Resources) != 1 {
		return errors.New("topic configuration unavailable")
	}
	resource := response.Resources[0]
	if resource.Error != nil || resource.ResourceName != auditconsumer.Topic {
		return errors.New("topic configuration unavailable")
	}
	for _, entry := range resource.ConfigEntries {
		if entry.ConfigName == "message.timestamp.type" && entry.ConfigValue == "LogAppendTime" {
			return nil
		}
	}
	return errors.New("topic requires LogAppendTime")
}

func (p *kafkaPublisher) Publish(ctx context.Context, payload []byte) (publicationReceipt, error) {
	var identity struct {
		SourceID string                    `json:"source_id"`
		Target   struct{ Type, ID string } `json:"target"`
	}
	if json.Unmarshal(payload, &identity) != nil {
		return publicationReceipt{}, errors.New("validated payload invalid")
	}
	message := kafka.Message{Key: []byte(identity.SourceID + "\x1f" + identity.Target.Type + "\x1f" + identity.Target.ID), Value: payload, Headers: []kafka.Header{{Key: auditvalidator.SchemaHeader, Value: []byte(auditvalidator.SchemaVersion)}}}
	err := p.writer.WriteMessages(ctx, message)
	if err != nil {
		return publicationReceipt{}, errors.New("kafka publish not acknowledged")
	}
	select {
	case complete := <-p.completion:
		if complete.err != nil || len(complete.messages) != 1 {
			return publicationReceipt{}, errors.New("kafka publish completion invalid")
		}
		acknowledged := complete.messages[0]
		return publicationReceipt{Topic: auditconsumer.Topic, Partition: acknowledged.Partition, Offset: acknowledged.Offset}, nil
	case <-ctx.Done():
		return publicationReceipt{}, errors.New("kafka publish completion unknown")
	}
}
func (p *kafkaPublisher) Close() error {
	err := p.writer.Close()
	p.transport.CloseIdleConnections()
	return err
}
