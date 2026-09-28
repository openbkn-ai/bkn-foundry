// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package operationaudit

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/auditpublisher"
	"github.com/openbkn-ai/bkn-foundry/comm-go/bkntrace/kafkasender"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
)

type KafkaRuntime struct {
	Publisher *auditpublisher.Publisher
	Producer  interface{ Close() error }
}

var ErrInvalidKafkaConfiguration = errors.New("vega Audit Kafka brokers and SASL PLAIN credentials are required")

func NewKafkaRuntimeFromEnv(telemetry *PublishTelemetry) (*KafkaRuntime, error) {
	brokers := strings.Split(strings.TrimSpace(os.Getenv("VEGA_AUDIT_KAFKA_BROKERS")), ",")
	clean := brokers[:0]
	for _, broker := range brokers {
		if broker = strings.TrimSpace(broker); broker != "" {
			clean = append(clean, broker)
		}
	}
	username := strings.TrimSpace(os.Getenv("VEGA_AUDIT_KAFKA_USERNAME"))
	password := os.Getenv("VEGA_AUDIT_KAFKA_PASSWORD")
	if len(clean) == 0 || os.Getenv("VEGA_AUDIT_KAFKA_SASL_MECHANISM") != "PLAIN" || username == "" || password == "" ||
		!validAuditEnvironment(strings.TrimSpace(os.Getenv("VEGA_AUDIT_ENVIRONMENT"))) {
		return nil, ErrInvalidKafkaConfiguration
	}
	producer, err := kafkasender.NewProducer(kafkasender.Config{
		Brokers: clean, Mechanism: "PLAIN", Username: username, Password: password,
	})
	if err != nil {
		return nil, fmt.Errorf("create Vega Audit Kafka producer: %w", err)
	}
	publisher, err := auditpublisher.New(kafkasender.NewAudit(producer), auditDeliveryObserver{telemetry: telemetry})
	if err != nil {
		_ = producer.Close()
		return nil, fmt.Errorf("create Vega Audit publisher: %w", err)
	}
	return &KafkaRuntime{Publisher: publisher, Producer: producer}, nil
}

func (r *KafkaRuntime) Close() error {
	if r == nil {
		return nil
	}
	if r.Publisher != nil {
		r.Publisher.Close()
	}
	if r.Producer != nil {
		return r.Producer.Close()
	}
	return nil
}

type auditDeliveryObserver struct{ telemetry *PublishTelemetry }

func (o auditDeliveryObserver) ObserveDelivery(delivery auditpublisher.Delivery) {
	if delivery.Outcome == auditpublisher.Delivered {
		o.telemetry.Observe("delivered", time.Now())
		return
	}
	reason := string(delivery.Outcome)
	if count, log := o.telemetry.Observe("dropped_"+reason, time.Now()); log {
		logger.Warnf("Vega Audit delivery coverage_gap: reason=%s count=%d", reason, count)
	}
}
