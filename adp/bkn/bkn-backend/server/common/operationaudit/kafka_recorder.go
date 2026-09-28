// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package operationaudit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/auditpublisher"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
)

type KafkaPublisher interface {
	TryPublish([]byte) auditpublisher.Disposition
}

// KafkaRecorder is the fail-open producer boundary for Backend management
// Audit. The HTTP middleware owns business-response isolation and logs errors
// as coverage gaps; this adapter never retries or writes a local Outbox.
type KafkaRecorder struct {
	publisher   KafkaPublisher
	environment string
	telemetry   *PublishTelemetry
}

func NewKafkaRecorder(publisher KafkaPublisher, environment string, telemetry ...*PublishTelemetry) *KafkaRecorder {
	recorder := &KafkaRecorder{publisher: publisher, environment: environment}
	if len(telemetry) > 0 {
		recorder.telemetry = telemetry[0]
	}
	return recorder
}

func (r *KafkaRecorder) Record(_ context.Context, entry Entry) error {
	if r == nil || r.publisher == nil {
		r.reportDrop("unavailable")
		return errors.New("Kafka Audit publisher is not configured")
	}
	value, err := BuildKafkaAuditRecord(entry, r.environment)
	if err != nil {
		r.reportDrop("invalid")
		return err
	}
	if result := r.publisher.TryPublish(value); result != auditpublisher.Accepted {
		r.reportDrop(strings.TrimPrefix(string(result), "dropped_"))
		return fmt.Errorf("Kafka Audit publish disposition: %s", result)
	}
	r.telemetry.Observe("accepted", time.Now())
	return nil
}

func (r *KafkaRecorder) reportDrop(reason string) {
	if r == nil || r.telemetry == nil {
		return
	}
	if count, log := r.telemetry.Observe("dropped_"+reason, time.Now()); log {
		logger.Warnf("Audit publish coverage_gap: reason=%s count=%d", reason, count)
	}
}
