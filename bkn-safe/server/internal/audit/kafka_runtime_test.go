package audit

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/accesslog"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/decisionlog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/auditpublisher"
	"github.com/openbkn-ai/bkn-foundry/comm-go/bkntrace/kafkasender"
)

func TestKafkaRuntimeWithoutConfigKeepsBusinessFailOpen(t *testing.T) {
	runtime := NewKafkaRuntimeFromEnv(func(string) string { return "" })
	if runtime == nil || runtime.Recorder == nil {
		t.Fatal("missing configuration must still provide a fail-open recorder")
	}
	if runtime.Publisher() != nil {
		t.Fatal("unavailable publisher must be a nil interface for access and decision recorders")
	}
	if err := runtime.Recorder.Record(context.Background(), Entry{
		RequestID: "req-safe-unconfigured", Method: "POST", Resource: "users", Action: "create", Status: 400,
	}); err == nil {
		t.Fatal("unconfigured publisher was not reported as a coverage gap")
	}
	runtime.Close()
}

func TestUnavailableKafkaKeepsAccessAndDecisionRecordersFailOpen(t *testing.T) {
	runtime := NewKafkaRuntimeFromEnv(func(key string) string {
		if key == "BKN_AUDIT_ENVIRONMENT" {
			return "production"
		}
		return ""
	})
	defer runtime.Close()
	access := accesslog.NewKafkaRecorder(runtime.Publisher(), "production")
	if err := access.Record(context.Background(), accesslog.Entry{Action: "login", Outcome: "success"}); err == nil {
		t.Fatal("unavailable Kafka must report the access audit gap")
	}
	decision := decisionlog.NewKafkaRecorder(runtime.Publisher(), "production", nil)
	decision.Record(decisionlog.Entry{ResourceType: "safe_admin", ResourceID: "console", Decision: decisionlog.DecisionDeny})
}

func TestKafkaRuntimeReconnectsAfterInitialBrokerFailure(t *testing.T) {
	attempts := 0
	runtime := newKafkaRuntimeFromEnv(func(key string) string {
		switch key {
		case "BKN_AUDIT_ENVIRONMENT":
			return "test"
		case "BKN_AUDIT_KAFKA_BROKERS":
			return "kafka:9092"
		case "BKN_AUDIT_KAFKA_SASL_MECHANISM":
			return "PLAIN"
		case "BKN_AUDIT_KAFKA_USERNAME":
			return "kafkauser"
		case "BKN_AUDIT_KAFKA_PASSWORD":
			return "secret"
		default:
			return ""
		}
	}, func(kafkasender.Config) (sarama.SyncProducer, error) {
		attempts++
		if attempts == 1 {
			return nil, errors.New("broker not ready")
		}
		return &testSyncProducer{}, nil
	}, time.Millisecond, 2)
	defer runtime.Close()

	if runtime.Publisher() == nil {
		t.Fatal("configured runtime must publish after a transient startup failure")
	}
	access := accesslog.NewKafkaRecorder(runtime.Publisher(), "test")
	if err := access.Record(context.Background(), accesslog.Entry{
		ActorID: "user-1", Action: "login", Outcome: "success", RequestID: "req-reconnect",
	}); err != nil {
		t.Fatalf("access recorder did not receive the initialized publisher: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("Kafka producer attempts = %d, want 2", attempts)
	}
}

type testSyncProducer struct{}

func (*testSyncProducer) SendMessage(*sarama.ProducerMessage) (int32, int64, error) { return 0, 0, nil }
func (*testSyncProducer) SendMessages([]*sarama.ProducerMessage) error              { return nil }
func (*testSyncProducer) Close() error                                              { return nil }
func (*testSyncProducer) TxnStatus() sarama.ProducerTxnStatusFlag                   { return 0 }
func (*testSyncProducer) IsTransactional() bool                                     { return false }
func (*testSyncProducer) BeginTxn() error                                           { return nil }
func (*testSyncProducer) CommitTxn() error                                          { return nil }
func (*testSyncProducer) AbortTxn() error                                           { return nil }
func (*testSyncProducer) AddOffsetsToTxn(map[string][]*sarama.PartitionOffsetMetadata, string) error {
	return nil
}
func (*testSyncProducer) AddOffsetsToTxnWithGroupMetadata(map[string][]*sarama.PartitionOffsetMetadata, *sarama.ConsumerGroupMetadata) error {
	return nil
}
func (*testSyncProducer) AddMessageToTxn(*sarama.ConsumerMessage, string, *string) error { return nil }
func (*testSyncProducer) AddMessageToTxnWithGroupMetadata(*sarama.ConsumerMessage, *sarama.ConsumerGroupMetadata, *string) error {
	return nil
}

func TestDeliveryObserverAttributesSharedPublisherAccessDelivery(t *testing.T) {
	telemetry := NewPublishTelemetry()
	safeDeliveryObserver{telemetry: telemetry}.ObserveDelivery(auditpublisher.Delivery{
		Record:  auditpublisher.Record{Key: []byte("bkn-safe-access\x1fsession\x1faccess-1")},
		Outcome: auditpublisher.Delivered,
	})
	recorder := httptest.NewRecorder()
	telemetry.ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(recorder.Body.String(), `audit_event_publish_total{source_id="bkn-safe-access",result="delivered",reason="none"} 1`) {
		t.Fatalf("shared delivery attribution = %q", recorder.Body.String())
	}
}
