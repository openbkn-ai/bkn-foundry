// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package conf

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

const EvidenceKafkaConsumerGroup = "bkn-trace-evidence-ledger-v1"

type KafkaConsumerConfig struct {
	Evidence KafkaTopicConsumerConfig
	Audit    KafkaTopicConsumerConfig
}

type KafkaTopicConsumerConfig struct {
	Enabled       bool
	Topic         string
	Group         string
	Brokers       []string
	SASLMechanism string
	Username      string
	Password      string
}

func NewKafkaConsumerConfig() (KafkaConsumerConfig, error) {
	var config KafkaConsumerConfig
	var err error
	config.Evidence.Enabled, err = optionalBoolEnv("BKN_TRACE_EVIDENCE_KAFKA_ENABLED")
	if err != nil {
		return KafkaConsumerConfig{}, err
	}
	config.Audit.Enabled, err = optionalBoolEnv("BKN_TRACE_AUDIT_KAFKA_ENABLED")
	if err != nil {
		return KafkaConsumerConfig{}, err
	}
	config.Evidence.Topic = "openbkn.evidence.v1"
	config.Audit.Topic = "openbkn.audit.v1"
	if config.Evidence.Enabled {
		config.Evidence, err = topicConsumerConfig("BKN_TRACE_EVIDENCE_KAFKA", config.Evidence.Topic, config.Evidence.Enabled)
		if err != nil {
			return KafkaConsumerConfig{}, err
		}
		if config.Evidence.Group == "" {
			config.Evidence.Group = EvidenceKafkaConsumerGroup
		}
		if config.Evidence.Group != EvidenceKafkaConsumerGroup {
			return KafkaConsumerConfig{}, fmt.Errorf("BKN_TRACE_EVIDENCE_KAFKA_GROUP must be %q", EvidenceKafkaConsumerGroup)
		}
	}
	if config.Audit.Enabled {
		config.Audit, err = topicConsumerConfig("BKN_TRACE_AUDIT_KAFKA", config.Audit.Topic, config.Audit.Enabled)
		if err != nil {
			return KafkaConsumerConfig{}, err
		}
		if config.Audit.Group == "" {
			return KafkaConsumerConfig{}, fmt.Errorf("BKN_TRACE_AUDIT_KAFKA_GROUP is required when Audit consumer is enabled")
		}
	}
	return config, nil
}

func topicConsumerConfig(prefix, topic string, enabled bool) (KafkaTopicConsumerConfig, error) {
	result := KafkaTopicConsumerConfig{Enabled: enabled, Topic: topic, Group: strings.TrimSpace(os.Getenv(prefix + "_GROUP"))}
	for _, broker := range strings.Split(strings.TrimSpace(os.Getenv(prefix+"_BROKERS")), ",") {
		broker = strings.TrimSpace(broker)
		if broker == "" {
			return KafkaTopicConsumerConfig{}, fmt.Errorf("%s_BROKERS must be a comma-separated list of host:port values", prefix)
		}
		host, port, err := net.SplitHostPort(broker)
		portNumber, portErr := strconv.Atoi(port)
		if err != nil || host == "" || portErr != nil || portNumber < 1 || portNumber > 65535 {
			return KafkaTopicConsumerConfig{}, fmt.Errorf("%s_BROKERS must contain valid host:port values", prefix)
		}
		result.Brokers = append(result.Brokers, broker)
	}
	if len(result.Brokers) == 0 {
		return KafkaTopicConsumerConfig{}, fmt.Errorf("%s_BROKERS is required when enabled", prefix)
	}
	result.SASLMechanism = strings.ToUpper(strings.TrimSpace(os.Getenv(prefix + "_SASL_MECHANISM")))
	result.Username, result.Password = os.Getenv(prefix+"_USERNAME"), os.Getenv(prefix+"_PASSWORD")
	switch result.SASLMechanism {
	case "PLAIN", "SCRAM-SHA-256", "SCRAM-SHA-512":
	default:
		return KafkaTopicConsumerConfig{}, fmt.Errorf("%s_SASL_MECHANISM is required and must be supported", prefix)
	}
	if result.Username == "" || result.Password == "" {
		return KafkaTopicConsumerConfig{}, fmt.Errorf("%s SASL credentials are required when enabled", prefix)
	}
	return result, nil
}
