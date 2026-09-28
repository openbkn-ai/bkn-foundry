// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package operationaudit

import (
	"errors"
	"testing"
)

func TestKafkaRuntimeRejectsStaticConfiguration(t *testing.T) {
	t.Setenv("BKN_AUDIT_KAFKA_BROKERS", "")
	t.Setenv("BKN_AUDIT_KAFKA_SASL_MECHANISM", "PLAIN")
	t.Setenv("BKN_AUDIT_KAFKA_USERNAME", "test-user")
	t.Setenv("BKN_AUDIT_KAFKA_PASSWORD", "test-password")
	_, err := NewKafkaRuntimeFromEnv(NewPublishTelemetry())
	if !errors.Is(err, ErrInvalidKafkaConfiguration) {
		t.Fatalf("missing brokers must be a static configuration error: %v", err)
	}

	t.Setenv("BKN_AUDIT_KAFKA_BROKERS", "127.0.0.1:1")
	t.Setenv("BKN_AUDIT_KAFKA_SASL_MECHANISM", "unsupported")
	_, err = NewKafkaRuntimeFromEnv(NewPublishTelemetry())
	if !errors.Is(err, ErrInvalidKafkaConfiguration) {
		t.Fatalf("invalid SASL mechanism must be a static configuration error: %v", err)
	}
}
