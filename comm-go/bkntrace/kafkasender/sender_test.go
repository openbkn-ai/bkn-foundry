package kafkasender

import (
	"context"
	"testing"

	"github.com/IBM/sarama"
	"github.com/openbkn-ai/bkn-foundry/comm-go/auditpublisher"
	"github.com/openbkn-ai/bkn-foundry/comm-go/bkntrace/evidencepublisher"
)

func TestNewProducerRejectsIncompleteKafkaCredentials(t *testing.T) {
	_, err := NewProducer(Config{Brokers: []string{"kafka:9092"}, Mechanism: "PLAIN", Username: "producer"})
	if err == nil {
		t.Fatal("NewProducer() error = nil, want invalid Kafka credential error")
	}
}

func TestSaramaConfigUsesPlainAndBoundedTransportTimeouts(t *testing.T) {
	config, err := newSaramaConfig(Config{Mechanism: "PLAIN", Username: "producer", Password: "secret"})
	if err != nil {
		t.Fatalf("newSaramaConfig() error = %v", err)
	}
	if !config.Net.SASL.Enable || config.Net.SASL.Mechanism != sarama.SASLTypePlaintext {
		t.Fatalf("SASL config = %#v", config.Net.SASL)
	}
	if config.Net.DialTimeout != transportTimeout || config.Net.ReadTimeout != transportTimeout || config.Net.WriteTimeout != transportTimeout || config.Producer.Timeout != transportTimeout {
		t.Fatalf("Kafka transport timeouts are not bounded to %s", transportTimeout)
	}
	if config.Producer.Retry.Max != 0 {
		t.Fatalf("transport retry max = %d, want 0", config.Producer.Retry.Max)
	}
}

func TestSaramaConfigRejectsUnsupportedSASLMechanism(t *testing.T) {
	_, err := newSaramaConfig(Config{Mechanism: "SCRAM-SHA-256", Username: "producer", Password: "secret"})
	if err == nil {
		t.Fatal("newSaramaConfig() error = nil, want unsupported mechanism error")
	}
}

type capturedProducer struct{ message *sarama.ProducerMessage }

func (p *capturedProducer) SendMessage(message *sarama.ProducerMessage) (int32, int64, error) {
	p.message = message
	return 0, 0, nil
}

func TestEvidenceSenderPreservesFrozenWireRecord(t *testing.T) {
	producer := &capturedProducer{}
	sender := NewEvidence(producer)
	record := evidencepublisher.Record{
		Key:   "stream:boot",
		Value: []byte(`{"event_id":"evt-1"}`),
		Headers: []evidencepublisher.Header{
			{Key: "content-type", Value: "application/json"},
			{Key: "capture_policy_revision", Value: "4"},
		},
	}

	if err := sender.Send(context.Background(), record); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if producer.message.Topic != evidencepublisher.Topic {
		t.Fatalf("topic = %q", producer.message.Topic)
	}
	key, keyErr := producer.message.Key.Encode()
	value, valueErr := producer.message.Value.Encode()
	if keyErr != nil || valueErr != nil || string(key) != record.Key || string(value) != string(record.Value) {
		t.Fatalf("message key/value were not preserved")
	}
	if len(producer.message.Headers) != len(record.Headers) {
		t.Fatalf("headers = %#v", producer.message.Headers)
	}
	for index, header := range record.Headers {
		if string(producer.message.Headers[index].Key) != header.Key || string(producer.message.Headers[index].Value) != header.Value {
			t.Fatalf("header[%d] = %#v", index, producer.message.Headers[index])
		}
	}
}

func TestAuditSenderPreservesFrozenWireRecord(t *testing.T) {
	producer := &capturedProducer{}
	sender := NewAudit(producer)
	record := auditpublisher.Record{
		Topic: auditpublisher.Topic,
		Key:   []byte("source\x1ftool\x1f1"), Value: []byte(`{"schema_version":"1.0"}`),
		Headers: []auditpublisher.Header{{Key: auditpublisher.SchemaVersionHeader, Value: []byte(auditpublisher.SchemaVersion)}},
	}

	if err := sender.Send(context.Background(), record); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	key, keyErr := producer.message.Key.Encode()
	if keyErr != nil || producer.message.Topic != auditpublisher.Topic || string(key) != string(record.Key) {
		t.Fatalf("topic/key were not preserved")
	}
	value, valueErr := producer.message.Value.Encode()
	if valueErr != nil || string(value) != string(record.Value) {
		t.Fatalf("value = %q", value)
	}
	if len(producer.message.Headers) != 1 || string(producer.message.Headers[0].Key) != auditpublisher.SchemaVersionHeader || string(producer.message.Headers[0].Value) != auditpublisher.SchemaVersion {
		t.Fatalf("headers = %#v", producer.message.Headers)
	}
}
