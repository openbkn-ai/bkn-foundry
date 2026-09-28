// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package operationaudit

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/auditpublisher"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type captureKafkaAuditPublisher struct {
	values      [][]byte
	disposition auditpublisher.Disposition
}

func (p *captureKafkaAuditPublisher) TryPublish(value []byte) auditpublisher.Disposition {
	p.values = append(p.values, append([]byte(nil), value...))
	return p.disposition
}

func validVegaAuditEntry() Entry {
	return Entry{EventID: "0194f4b8-9a79-7c7f-881e-0f0a97107d22", EventTime: time.Now().UTC(),
		ActorID: "user-1", ActorName: "Vega Manager", ActorType: "user", AuthMethod: "oauth",
		RequestID: "req-vega-1", SourceChannel: "api", Method: "PUT", HTTPStatus: 204,
		Action: "update", TargetType: "catalog", TargetID: "catalog-1", Outcome: "success",
		ChangedFields: []string{"name"},
	}
}

func TestKafkaRecorderRecord(t *testing.T) {
	t.Run("accepted record is a valid Kafka Audit payload", func(t *testing.T) {
		publisher := &captureKafkaAuditPublisher{disposition: auditpublisher.Accepted}
		telemetry := NewPublishTelemetry()
		recorder := NewKafkaRecorder(publisher, "test", telemetry)
		require.NoError(t, recorder.Record(context.Background(), validVegaAuditEntry()))
		require.Len(t, publisher.values, 1)
		_, err := auditpublisher.BuildRecord(publisher.values[0])
		require.NoError(t, err)
		metrics := httptest.NewRecorder()
		telemetry.ServeHTTP(metrics, nil)
		assert.Contains(t, metrics.Body.String(), `audit_event_publish_total{source_id="vega",result="accepted",reason="none"} 1`)
	})
	t.Run("queue drop is visible without a local write", func(t *testing.T) {
		publisher := &captureKafkaAuditPublisher{disposition: auditpublisher.DroppedQueueFull}
		telemetry := NewPublishTelemetry()
		recorder := NewKafkaRecorder(publisher, "test", telemetry)
		require.Error(t, recorder.Record(context.Background(), validVegaAuditEntry()))
		metrics := httptest.NewRecorder()
		telemetry.ServeHTTP(metrics, nil)
		assert.Contains(t, metrics.Body.String(), `audit_event_publish_total{source_id="vega",result="dropped",reason="queue_full"} 1`)
	})
}

func TestKafkaRecorderRecoversAfterPublisherBecomesAvailable(t *testing.T) {
	telemetry := NewPublishTelemetry()
	recorder := NewKafkaRecorder(nil, "test", telemetry)
	require.Error(t, recorder.Record(context.Background(), validVegaAuditEntry()))
	publisher := &captureKafkaAuditPublisher{disposition: auditpublisher.Accepted}
	recorder.SetPublisher(publisher)
	require.NoError(t, recorder.Record(context.Background(), validVegaAuditEntry()))
	require.Len(t, publisher.values, 1)
}
