package audit

import (
	"log/slog"
	"strings"
	"time"

	"github.com/IBM/sarama"
	"github.com/openbkn-ai/bkn-foundry/comm-go/auditpublisher"
	"github.com/openbkn-ai/bkn-foundry/comm-go/bkntrace/kafkasender"
)

// KafkaRuntime owns the bounded Safe Audit producer and its Kafka transport.
type KafkaRuntime struct {
	Recorder  *KafkaRecorder
	Telemetry *PublishTelemetry
	publisher *auditpublisher.Publisher
	producer  interface{ Close() error }
}

const kafkaStartupAttempts = 5

func NewKafkaRuntimeFromEnv(getenv func(string) string) *KafkaRuntime {
	return newKafkaRuntimeFromEnv(getenv, kafkasender.NewProducer, time.Second, kafkaStartupAttempts)
}

func newKafkaRuntimeFromEnv(getenv func(string) string, newProducer func(kafkasender.Config) (sarama.SyncProducer, error), retryEvery time.Duration, attempts int) *KafkaRuntime {
	environment := strings.TrimSpace(getenv("BKN_AUDIT_ENVIRONMENT"))
	telemetry := NewPublishTelemetry()
	runtime := &KafkaRuntime{Recorder: NewKafkaRecorder(nil, environment, telemetry), Telemetry: telemetry}
	var brokers []string
	for _, broker := range strings.Split(getenv("BKN_AUDIT_KAFKA_BROKERS"), ",") {
		if broker = strings.TrimSpace(broker); broker != "" {
			brokers = append(brokers, broker)
		}
	}
	username := strings.TrimSpace(getenv("BKN_AUDIT_KAFKA_USERNAME"))
	password := getenv("BKN_AUDIT_KAFKA_PASSWORD")
	if len(brokers) == 0 || strings.TrimSpace(getenv("BKN_AUDIT_KAFKA_SASL_MECHANISM")) != "PLAIN" || username == "" || password == "" {
		slog.Error("safe audit coverage gap", "reason", "kafka_not_configured")
		return runtime
	}
	if attempts < 1 {
		attempts = 1
	}
	if retryEvery <= 0 {
		retryEvery = time.Second
	}
	config := kafkasender.Config{Brokers: brokers, Mechanism: "PLAIN", Username: username, Password: password}
	var producer sarama.SyncProducer
	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		producer, err = newProducer(config)
		if err == nil {
			break
		}
		if attempt < attempts {
			slog.Error("safe audit coverage gap", "reason", "kafka_init_retry", "attempt", attempt, "error", err)
			time.Sleep(retryEvery)
		}
	}
	if err != nil {
		slog.Error("safe audit coverage gap", "reason", "kafka_init_failed", "attempts", attempts, "error", err)
		return runtime
	}
	publisher, err := auditpublisher.New(kafkasender.NewAudit(producer), safeDeliveryObserver{telemetry: telemetry})
	if err != nil {
		_ = producer.Close()
		slog.Error("safe audit coverage gap", "reason", "publisher_init_failed", "error", err)
		return runtime
	}
	runtime.publisher, runtime.producer = publisher, producer
	runtime.Recorder = NewKafkaRecorder(publisher, environment, telemetry)
	return runtime
}

func (r *KafkaRuntime) Close() {
	if r == nil {
		return
	}
	if r.publisher != nil {
		r.publisher.Close()
	}
	if r.producer != nil {
		_ = r.producer.Close()
	}
}

// Publisher exposes the narrow, credential-free producer boundary so Safe's
// registered Audit sources share one bounded queue and delivery worker pool.
func (r *KafkaRuntime) Publisher() KafkaPublisher {
	if r == nil || r.publisher == nil {
		return nil
	}
	return r.publisher
}

type safeDeliveryObserver struct{ telemetry *PublishTelemetry }

func (o safeDeliveryObserver) ObserveDelivery(delivery auditpublisher.Delivery) {
	sourceID := "bkn-safe-admin"
	if parts := strings.SplitN(string(delivery.Record.Key), "\x1f", 2); len(parts) == 2 && parts[0] != "" {
		sourceID = parts[0]
	}
	if delivery.Outcome == auditpublisher.Delivered {
		o.telemetry.ObserveForSource(sourceID, "delivered")
	} else {
		o.telemetry.ObserveForSource(sourceID, "dropped_"+string(delivery.Outcome))
	}
	if delivery.Outcome != auditpublisher.Delivered {
		slog.Error("safe audit delivery coverage gap", "outcome", delivery.Outcome, "attempts", delivery.Attempts, "error", delivery.Err)
	}
}
