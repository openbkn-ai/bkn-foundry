package bknaudit

import (
	"context"
	"os"
	"strings"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/auditpublisher"
	"github.com/openbkn-ai/bkn-foundry/comm-go/bkntrace/kafkasender"
)

var (
	configuredOnce sync.Once
	configured     auditpublisherRuntime
)

type auditpublisherRuntime struct {
	publisher *auditpublisher.Publisher
	producer  interface{ Close() error }
}

// ConfiguredPublisher builds the process-wide fail-open Audit v1 publisher.
func ConfiguredPublisher(logger interfaces.Logger) *auditpublisher.Publisher {
	configuredOnce.Do(func() {
		get := func(key string) string { return strings.TrimSpace(os.Getenv(key)) }
		brokers := strings.Split(get("BKN_AUDIT_KAFKA_BROKERS"), ",")
		clean := brokers[:0]
		for _, broker := range brokers {
			if broker = strings.TrimSpace(broker); broker != "" {
				clean = append(clean, broker)
			}
		}
		username, password := get("BKN_AUDIT_KAFKA_USERNAME"), os.Getenv("BKN_AUDIT_KAFKA_PASSWORD")
		if len(clean) == 0 || get("BKN_AUDIT_KAFKA_SASL_MECHANISM") != "PLAIN" || username == "" || password == "" {
			logger.Warn("audit coverage_gap: Kafka publisher is not configured")
			return
		}
		producer, err := kafkasender.NewProducer(kafkasender.Config{Brokers: clean, Mechanism: "PLAIN", Username: username, Password: password})
		if err != nil {
			logger.Warnf("audit coverage_gap: Kafka publisher initialization failed: %v", err)
			return
		}
		publisher, err := auditpublisher.New(kafkasender.NewAudit(producer), auditDeliveryObserver{logger: logger})
		if err != nil {
			_ = producer.Close()
			logger.Warnf("audit coverage_gap: publisher initialization failed: %v", err)
			return
		}
		configured = auditpublisherRuntime{publisher: publisher, producer: producer}
	})
	return configured.publisher
}

// ClosePublisher drains the bounded publisher and closes its Kafka transport.
func ClosePublisher(ctx context.Context) {
	_ = ctx
	if configured.publisher != nil {
		configured.publisher.Close()
	}
	if configured.producer != nil {
		_ = configured.producer.Close()
	}
}

type auditDeliveryObserver struct{ logger interfaces.Logger }

func (o auditDeliveryObserver) ObserveDelivery(delivery auditpublisher.Delivery) {
	if delivery.Outcome != auditpublisher.Delivered {
		o.logger.Warnf("audit delivery coverage_gap: outcome=%s attempts=%d", delivery.Outcome, delivery.Attempts)
	}
}
