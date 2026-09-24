// Package kafkasender adapts frozen Evidence and Audit records to Kafka wire
// messages. Queueing, retries, and business disposition remain owned by the
// respective publisher packages.
package kafkasender

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/IBM/sarama"
	"github.com/openbkn-ai/bkn-foundry/comm-go/auditpublisher"
	"github.com/openbkn-ai/bkn-foundry/comm-go/bkntrace/evidencepublisher"
)

// Producer is the narrow Kafka transport dependency used by the adapters.
type Producer interface {
	SendMessage(*sarama.ProducerMessage) (partition int32, offset int64, err error)
}

// Config is the deployment-supplied Kafka transport configuration. It is
// intentionally limited to connection/authentication concerns: publisher
// queueing and retry behavior are frozen in the caller's publisher.
type Config struct {
	Brokers   []string
	Mechanism string
	Username  string
	Password  string
}

const transportTimeout = 3 * time.Second

// NewProducer creates the synchronous Kafka transport used from publisher
// workers. A missing or invalid connection configuration is a startup error;
// it must never select an HTTP fallback.
func NewProducer(config Config) (sarama.SyncProducer, error) {
	if len(config.Brokers) == 0 || strings.TrimSpace(config.Mechanism) == "" || strings.TrimSpace(config.Username) == "" || strings.TrimSpace(config.Password) == "" {
		return nil, errors.New("invalid Kafka sender config")
	}
	for _, broker := range config.Brokers {
		if strings.TrimSpace(broker) == "" {
			return nil, errors.New("invalid Kafka sender config")
		}
	}

	saramaConfig, err := newSaramaConfig(config)
	if err != nil {
		return nil, err
	}
	return sarama.NewSyncProducer(config.Brokers, saramaConfig)
}

func newSaramaConfig(config Config) (*sarama.Config, error) {
	if !strings.EqualFold(strings.TrimSpace(config.Mechanism), string(sarama.SASLTypePlaintext)) {
		return nil, errors.New("unsupported Kafka SASL mechanism")
	}
	saramaConfig := sarama.NewConfig()
	saramaConfig.Net.SASL.Enable = true
	saramaConfig.Net.SASL.Mechanism = sarama.SASLTypePlaintext
	saramaConfig.Net.SASL.User = config.Username
	saramaConfig.Net.SASL.Password = config.Password
	saramaConfig.Net.DialTimeout = transportTimeout
	saramaConfig.Net.ReadTimeout = transportTimeout
	saramaConfig.Net.WriteTimeout = transportTimeout
	saramaConfig.Producer.RequiredAcks = sarama.WaitForAll
	saramaConfig.Producer.Return.Successes = true
	saramaConfig.Producer.Retry.Max = 0
	saramaConfig.Producer.Timeout = transportTimeout
	saramaConfig.Net.MaxOpenRequests = 1
	return saramaConfig, nil
}

type evidenceSender struct {
	producer Producer
	topic    string
}

// NewEvidence returns the Evidence publisher Sender for its frozen topic.
func NewEvidence(producer Producer) evidencepublisher.Sender {
	return evidenceSender{producer: producer, topic: evidencepublisher.Topic}
}

func (s evidenceSender) Send(_ context.Context, record evidencepublisher.Record) error {
	headers := make([]sarama.RecordHeader, 0, len(record.Headers))
	for _, header := range record.Headers {
		headers = append(headers, sarama.RecordHeader{Key: []byte(header.Key), Value: []byte(header.Value)})
	}
	_, _, err := s.producer.SendMessage(&sarama.ProducerMessage{
		Topic: s.topic, Key: sarama.StringEncoder(record.Key), Value: sarama.ByteEncoder(record.Value), Headers: headers,
	})
	return err
}

type auditSender struct{ producer Producer }

// NewAudit returns the Audit publisher Sender for its frozen record type.
func NewAudit(producer Producer) auditpublisher.Sender {
	return auditSender{producer: producer}
}

func (s auditSender) Send(_ context.Context, record auditpublisher.Record) error {
	if record.Topic != auditpublisher.Topic {
		return errors.New("invalid audit Kafka topic")
	}
	headers := make([]sarama.RecordHeader, 0, len(record.Headers))
	for _, header := range record.Headers {
		headers = append(headers, sarama.RecordHeader{Key: []byte(header.Key), Value: append([]byte(nil), header.Value...)})
	}
	_, _, err := s.producer.SendMessage(&sarama.ProducerMessage{
		Topic: record.Topic, Key: sarama.ByteEncoder(record.Key), Value: sarama.ByteEncoder(record.Value), Headers: headers,
	})
	return err
}
