// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package kafka

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl"
	"github.com/segmentio/kafka-go/sasl/plain"
	"github.com/segmentio/kafka-go/sasl/scram"

	"vega-backend/common"
	"vega-backend/interfaces"
)

var (
	kAccessOnce sync.Once
	kAccess     interfaces.KafkaAccess
)

type kafkaAccess struct {
	appSetting *common.AppSetting
}

func NewKafkaAccess(appSetting *common.AppSetting) interfaces.KafkaAccess {
	kAccessOnce.Do(func() {
		kAccess = &kafkaAccess{
			appSetting: appSetting,
		}
	})

	return kAccess
}

// The getSASLMechanism acquires the SASL mechanism based on the configuration
func (ka *kafkaAccess) getSASLMechanism() sasl.Mechanism {
	switch ka.appSetting.MQSetting.Auth.Mechanism {
	case "PLAIN":
		return plain.Mechanism{
			Username: ka.appSetting.MQSetting.Auth.Username,
			Password: ka.appSetting.MQSetting.Auth.Password,
		}
	case "SCRAM-SHA-256":
		if mechanism, err := scram.Mechanism(scram.SHA256, ka.appSetting.MQSetting.Auth.Username, ka.appSetting.MQSetting.Auth.Password); err == nil {
			return mechanism
		} else {
			logger.Errorf("Failed to create SCRAM-SHA-256 mechanism: %v", err)
			// Fallback to PLAIN if SCRAM fails
			return plain.Mechanism{
				Username: ka.appSetting.MQSetting.Auth.Username,
				Password: ka.appSetting.MQSetting.Auth.Password,
			}
		}
	case "SCRAM-SHA-512":
		if mechanism, err := scram.Mechanism(scram.SHA512, ka.appSetting.MQSetting.Auth.Username, ka.appSetting.MQSetting.Auth.Password); err == nil {
			return mechanism
		} else {
			logger.Errorf("Failed to create SCRAM-SHA-512 mechanism: %v", err)
			// Fallback to PLAIN if SCRAM fails
			return plain.Mechanism{
				Username: ka.appSetting.MQSetting.Auth.Username,
				Password: ka.appSetting.MQSetting.Auth.Password,
			}
		}
	default:
		// Default to PLAIN if mechanism is not specified or unsupported
		return plain.Mechanism{
			Username: ka.appSetting.MQSetting.Auth.Username,
			Password: ka.appSetting.MQSetting.Auth.Password,
		}
	}
}

// getSASLDialer creates a Dialer and decides whether to use SASL authentication based on the configuration
func (ka *kafkaAccess) getSASLDialer() *kafka.Dialer {
	dialer := &kafka.Dialer{
		Timeout:   10 * time.Second,
		DualStack: true,
	}

	// SASL authentication is only used when the authentication information exists
	if ka.appSetting.MQSetting.Auth.Username != "" && ka.appSetting.MQSetting.Auth.Password != "" {
		mechanism := ka.getSASLMechanism()
		dialer.SASLMechanism = mechanism
	}

	return dialer
}

// Get the broker address with getBrokerAddress
func (ka *kafkaAccess) getBrokerAddress() string {
	return fmt.Sprintf("%s:%d", ka.appSetting.MQSetting.MQHost, ka.appSetting.MQSetting.MQPort)
}

// NewReader creates consumers
func (ka *kafkaAccess) NewReader(ctx context.Context, topic string, groupID string) (*kafka.Reader, error) {
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:     []string{ka.getBrokerAddress()},
		Topic:       topic,
		GroupID:     groupID,
		Dialer:      ka.getSASLDialer(),
		MaxBytes:    interfaces.MAX_MESSAGE_BYTES,
		StartOffset: kafka.FirstOffset,
		// Do not set the CommitInterval and use manual commit
	})

	logger.Debugf("Created reader for topic %s with groupID %s on cluster %s", topic, groupID, ka.appSetting.MQSetting.MQHost)
	return r, nil
}

// CloseReader closes consumers
func (ka *kafkaAccess) CloseReader(r *kafka.Reader) {
	if r != nil {
		if err := r.Close(); err != nil {
			logger.Errorf("Failed to close reader: %v", err)
		}
	}
}

// NewWriter creates producers
func (ka *kafkaAccess) NewWriter(ctx context.Context, topic string) (*kafka.Writer, error) {
	w := &kafka.Writer{
		Addr:         kafka.TCP(ka.getBrokerAddress()),
		Topic:        topic,
		BatchSize:    1,
		BatchTimeout: 10 * time.Millisecond,
		WriteTimeout: 10 * time.Second,
		ReadTimeout:  10 * time.Second,
		RequiredAcks: kafka.RequireAll,
	}

	// SASL authentication is only used when the authentication information exists
	if ka.appSetting.MQSetting.Auth.Username != "" && ka.appSetting.MQSetting.Auth.Password != "" {
		mechanism := ka.getSASLMechanism()
		w.Transport = &kafka.Transport{
			SASL: mechanism,
		}
	}

	logger.Debugf("Created writer for topic %s on cluster %s", topic, ka.appSetting.MQSetting.MQHost)
	return w, nil
}

// CloseWriter closes the producer
func (ka *kafkaAccess) CloseWriter(w *kafka.Writer) {
	if w != nil {
		if err := w.Close(); err != nil {
			logger.Errorf("Failed to close writer: %v", err)
		}
	}
}

// WriteMessages sends messages
func (ka *kafkaAccess) WriteMessages(ctx context.Context, w *kafka.Writer, msgs ...kafka.Message) error {
	if len(msgs) == 0 {
		return nil
	}

	logger.Debugf("Preparing to write %d messages to topic %s", len(msgs), w.Topic)

	err := w.WriteMessages(ctx, msgs...)
	if err != nil {
		logger.Errorf("Failed to write messages to topic %s: %v", w.Topic, err)
		return err
	}

	logger.Debugf("Successfully wrote %d messages to topic %s", len(msgs), w.Topic)
	return nil
}

// ReadMessage consumes the message
func (ka *kafkaAccess) ReadMessage(ctx context.Context, r *kafka.Reader) (kafka.Message, error) {
	msg, err := r.ReadMessage(ctx)
	if err != nil {
		return kafka.Message{}, err
	}
	return msg, nil
}

// CommitMessages manually commits the shift
func (ka *kafkaAccess) CommitMessages(ctx context.Context, r *kafka.Reader, msgs ...kafka.Message) error {
	if err := r.CommitMessages(ctx, msgs...); err != nil {
		logger.Errorf("Failed to commit messages: %v", err)
		return err
	}
	logger.Debugf("Successfully committed %d messages", len(msgs))
	return nil
}

// CreateTopic: Create a topic
func (ka *kafkaAccess) CreateTopic(ctx context.Context, topicName string) error {
	logger.Infof("Creating topic %s", topicName)
	// Use a connection with SASL certification
	dialer := ka.getSASLDialer()
	conn, err := dialer.DialContext(ctx, "tcp", ka.getBrokerAddress())
	if err != nil {
		logger.Errorf("Failed to dial kafka with SASL: %v", err)
		return err
	}
	defer func() { _ = conn.Close() }()

	controller, err := conn.Controller()
	if err != nil {
		logger.Errorf("Failed to get controller: %v", err)
		return err
	}

	controllerConn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(controller.Host, fmt.Sprintf("%d", controller.Port)))
	if err != nil {
		logger.Errorf("Failed to dial controller: %v", err)
		return err
	}
	defer func() { _ = controllerConn.Close() }()

	topicConfigs := []kafka.TopicConfig{
		{
			Topic:             topicName,
			NumPartitions:     -1,
			ReplicationFactor: -1,
		},
	}

	err = controllerConn.CreateTopics(topicConfigs...)
	if err != nil {
		// Ignore the existing errors in the topic
		if err.Error() == "Topic with this name already exists" {
			logger.Infof("Topic %s already exists", topicName)
			return nil
		}
		logger.Errorf("Failed to create topic %s: %v", topicName, err)
		return err
	}

	logger.Infof("Created topic %s", topicName)
	return nil
}
