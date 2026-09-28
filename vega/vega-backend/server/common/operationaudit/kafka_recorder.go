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
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/auditpublisher"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
)

type KafkaPublisher interface {
	TryPublish([]byte) auditpublisher.Disposition
}

// KafkaRecorder is Vega's fail-open management Audit producer boundary.
type KafkaRecorder struct {
	mu          sync.RWMutex
	publisher   KafkaPublisher
	environment string
	telemetry   *PublishTelemetry
}

func NewKafkaRecorder(publisher KafkaPublisher, environment string, telemetry *PublishTelemetry) *KafkaRecorder {
	return &KafkaRecorder{publisher: publisher, environment: environment, telemetry: telemetry}
}

// SetPublisher restores or drains the active producer without changing the
// HTTP request path's bounded, fail-open behavior.
func (r *KafkaRecorder) SetPublisher(publisher KafkaPublisher) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.publisher = publisher
	r.mu.Unlock()
}

func (r *KafkaRecorder) Record(_ context.Context, entry Entry) error {
	if r == nil {
		return errors.New("kafka Audit recorder is not configured")
	}
	r.mu.RLock()
	publisher := r.publisher
	r.mu.RUnlock()
	if publisher == nil {
		r.reportDrop("unavailable", "")
		return errors.New("kafka Audit publisher is not configured")
	}
	value, err := BuildKafkaAuditRecord(entry, r.environment)
	if err != nil {
		var fieldErr *InvalidAuditFieldError
		if errors.As(err, &fieldErr) {
			r.reportDrop("invalid", fieldErr.Field)
		} else {
			r.reportDrop("invalid", "")
		}
		return err
	}
	if result := publisher.TryPublish(value); result != auditpublisher.Accepted {
		r.reportDrop(strings.TrimPrefix(string(result), "dropped_"), "")
		return fmt.Errorf("kafka Audit publish disposition: %s", result)
	}
	r.telemetry.Observe("accepted", time.Now())
	return nil
}

func (r *KafkaRecorder) reportDrop(reason, field string) {
	if r == nil || r.telemetry == nil {
		return
	}
	if count, log := r.telemetry.Observe("dropped_"+reason, time.Now()); log {
		if field != "" {
			logger.Warnf("Vega Audit coverage_gap: reason=%s field=%s count=%d", reason, field, count)
		} else {
			logger.Warnf("Vega Audit coverage_gap: reason=%s count=%d", reason, count)
		}
	}
}
