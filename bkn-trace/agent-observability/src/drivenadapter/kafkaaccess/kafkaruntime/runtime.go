// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

// Package kafkaruntime owns one independently configured Kafka consumer
// lifecycle. It uses synchronous, manual offset commits after its processor
// reports an authoritative terminal result.
package kafkaruntime

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/IBM/sarama"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/conf"
	kafka "github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl/plain"
	kafkascram "github.com/segmentio/kafka-go/sasl/scram"
	xdgscram "github.com/xdg-go/scram"
)

type Reader interface {
	FetchMessage(context.Context) (kafka.Message, error)
	CommitMessages(context.Context, ...kafka.Message) error
	Close() error
}

type ReaderFactory func(conf.KafkaConsumerConfig, conf.KafkaTopicConsumerConfig) (Reader, error)
type Processor func(context.Context, kafka.Message) error

type State struct {
	Enabled bool
	Ready   bool
	Reason  string
}

type Runtime struct {
	reader   Reader
	process  Processor
	stateMu  sync.RWMutex
	state    State
	stopPoll context.CancelFunc
	stopWork context.CancelFunc
	done     chan struct{}
	started  bool
	startMu  sync.Mutex
}

func New(config conf.KafkaConsumerConfig, topic conf.KafkaTopicConsumerConfig, process Processor) (*Runtime, error) {
	return NewWithFactory(config, topic, process, KafkaReaderFactory)
}

func NewWithFactory(config conf.KafkaConsumerConfig, topic conf.KafkaTopicConsumerConfig, process Processor, factory ReaderFactory) (*Runtime, error) {
	if !topic.Enabled {
		return &Runtime{state: State{Enabled: false, Ready: false, Reason: "disabled"}}, nil
	}
	if process == nil || factory == nil {
		return nil, errors.New("enabled Kafka consumer requires processor and reader factory")
	}
	reader, err := factory(config, topic)
	if err != nil {
		return nil, fmt.Errorf("create Kafka consumer reader: %w", err)
	}
	return &Runtime{
		reader: reader, process: process, state: State{Enabled: true, Reason: "starting"}, done: make(chan struct{}),
	}, nil
}

func KafkaReaderFactory(config conf.KafkaConsumerConfig, topic conf.KafkaTopicConsumerConfig) (Reader, error) {
	if err := VerifyTopicLogAppendTime(topic); err != nil {
		return nil, err
	}
	dialer := &kafka.Dialer{}
	switch topic.SASLMechanism {
	case "":
	case "PLAIN":
		dialer.SASLMechanism = plain.Mechanism{Username: topic.Username, Password: topic.Password}
	case "SCRAM-SHA-256":
		mechanism, err := kafkascram.Mechanism(kafkascram.SHA256, topic.Username, topic.Password)
		if err != nil {
			return nil, errors.New("initialize Kafka SCRAM-SHA-256 mechanism")
		}
		dialer.SASLMechanism = mechanism
	case "SCRAM-SHA-512":
		mechanism, err := kafkascram.Mechanism(kafkascram.SHA512, topic.Username, topic.Password)
		if err != nil {
			return nil, errors.New("initialize Kafka SCRAM-SHA-512 mechanism")
		}
		dialer.SASLMechanism = mechanism
	default:
		return nil, errors.New("unsupported Kafka SASL mechanism")
	}
	return kafka.NewReader(kafka.ReaderConfig{
		Brokers: topic.Brokers, Topic: topic.Topic, GroupID: topic.Group,
		CommitInterval: 0, MinBytes: 1, MaxBytes: 1 << 20, Dialer: dialer,
	}), nil
}

// VerifyTopicLogAppendTime refuses to start consumption unless the broker's
// effective topic setting is readable and exactly LogAppendTime. kafka-go does
// not expose the timestamp type on each fetched record: this is startup
// configuration evidence, not per-record proof. With that setting unchanged,
// the record timestamp exposed by Kafka is broker-assigned append time.
func VerifyTopicLogAppendTime(topic conf.KafkaTopicConsumerConfig) error {
	config := sarama.NewConfig()
	config.Version = sarama.V2_8_0_0
	if topic.SASLMechanism != "" {
		config.Net.SASL.Enable = true
		config.Net.SASL.User, config.Net.SASL.Password = topic.Username, topic.Password
		switch topic.SASLMechanism {
		case "PLAIN":
			config.Net.SASL.Mechanism = sarama.SASLTypePlaintext
		case "SCRAM-SHA-256":
			config.Net.SASL.Mechanism = sarama.SASLTypeSCRAMSHA256
			config.Net.SASL.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient { return &scramClient{hash: xdgscram.SHA256} }
		case "SCRAM-SHA-512":
			config.Net.SASL.Mechanism = sarama.SASLTypeSCRAMSHA512
			config.Net.SASL.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient { return &scramClient{hash: xdgscram.SHA512} }
		default:
			return errors.New("unsupported Kafka SASL mechanism")
		}
	}
	admin, err := sarama.NewClusterAdmin(topic.Brokers, config)
	if err != nil {
		return errors.New("connect Kafka Admin API to verify topic timestamp policy")
	}
	defer func() { _ = admin.Close() }()
	resources, err := admin.DescribeConfigs([]*sarama.ConfigResource{{Type: sarama.TopicResource, Name: topic.Topic}}, sarama.DescribeConfigsOptions{})
	if err != nil {
		return errors.New("kafka topic configuration is not readable; LogAppendTime cannot be proven")
	}
	if len(resources) != 1 || resources[0].Name != topic.Topic || resources[0].ErrorCode != sarama.ErrNoError {
		return errors.New("kafka topic configuration is not readable; LogAppendTime cannot be proven")
	}
	return verifyLogAppendTimeSetting(resources[0].Configs)
}

func verifyLogAppendTimeSetting(entries []sarama.ConfigEntry) error {
	for _, entry := range entries {
		if entry.Name == "message.timestamp.type" {
			if entry.Value == "LogAppendTime" {
				return nil
			}
			return errors.New("kafka topic message.timestamp.type is not LogAppendTime")
		}
	}
	return errors.New("kafka topic configuration omitted message.timestamp.type")
}

type scramClient struct {
	hash         xdgscram.HashGeneratorFcn
	conversation *xdgscram.ClientConversation
}

func (c *scramClient) Begin(username, password, authzID string) error {
	client, err := c.hash.NewClient(username, password, authzID)
	if err != nil {
		return err
	}
	c.conversation = client.NewConversation()
	return nil
}
func (c *scramClient) Step(challenge string) (string, error) { return c.conversation.Step(challenge) }
func (c *scramClient) Done() bool                            { return c.conversation != nil && c.conversation.Done() }

func (r *Runtime) Start(ctx context.Context) error {
	r.startMu.Lock()
	defer r.startMu.Unlock()
	if !r.state.Enabled {
		return nil
	}
	if r.started {
		return errors.New("kafka consumer runtime already started")
	}
	r.started = true
	pollCtx, stopPoll := context.WithCancel(ctx)
	processCtx, stopWork := context.WithCancel(context.Background())
	r.stopPoll, r.stopWork = stopPoll, stopWork
	go r.run(pollCtx, processCtx)
	return nil
}

func (r *Runtime) run(pollCtx, processCtx context.Context) {
	defer close(r.done)
	r.setState(true, "polling")
	failureReason := ""
	for pollCtx.Err() == nil {
		message, err := r.reader.FetchMessage(pollCtx)
		if err != nil {
			if pollCtx.Err() != nil {
				break
			}
			failureReason = "fetch_failed"
			r.setState(false, failureReason)
			break
		}
		if pollCtx.Err() != nil {
			// FetchMessage may return a buffered record concurrently with stop.
			// Leave it uncommitted for the next process rather than accepting it.
			break
		}
		if err := r.process(processCtx, message); err != nil {
			failureReason = "ledger_decision_pending"
			r.setState(false, failureReason)
			break
		}
		if err := r.reader.CommitMessages(processCtx, message); err != nil {
			failureReason = "offset_commit_failed"
			r.setState(false, failureReason)
			break
		}
		if pollCtx.Err() != nil {
			break
		}
	}
	if failureReason == "" {
		r.setState(false, "stopped")
	}
	_ = r.reader.Close()
	r.stopWork()
}

// Shutdown first interrupts FetchMessage and lets the current processor and
// synchronous commit finish. A deadline failure is surfaced and never treated
// as a successful drain.
func (r *Runtime) Shutdown(ctx context.Context) error {
	if !r.state.Enabled {
		return nil
	}
	r.startMu.Lock()
	started := r.started
	stopPoll, stopWork := r.stopPoll, r.stopWork
	r.startMu.Unlock()
	if !started {
		return r.reader.Close()
	}
	stopPoll()
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		stopWork()
		_ = r.reader.Close()
		return ctx.Err()
	}
}

func (r *Runtime) State() State {
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	return r.state
}

func (r *Runtime) setState(ready bool, reason string) {
	r.stateMu.Lock()
	r.state.Ready, r.state.Reason = ready, reason
	r.stateMu.Unlock()
}
