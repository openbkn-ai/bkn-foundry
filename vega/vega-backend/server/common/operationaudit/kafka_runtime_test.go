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
	t.Setenv("VEGA_AUDIT_KAFKA_BROKERS", "")
	t.Setenv("VEGA_AUDIT_KAFKA_SASL_MECHANISM", "PLAIN")
	t.Setenv("VEGA_AUDIT_KAFKA_USERNAME", "test-user")
	t.Setenv("VEGA_AUDIT_KAFKA_PASSWORD", "test-password")
	_, err := NewKafkaRuntimeFromEnv(NewPublishTelemetry())
	if !errors.Is(err, ErrInvalidKafkaConfiguration) {
		t.Fatalf("missing brokers must be static configuration error: %v", err)
	}
	t.Setenv("VEGA_AUDIT_KAFKA_BROKERS", "127.0.0.1:1")
	t.Setenv("VEGA_AUDIT_KAFKA_SASL_MECHANISM", "unsupported")
	_, err = NewKafkaRuntimeFromEnv(NewPublishTelemetry())
	if !errors.Is(err, ErrInvalidKafkaConfiguration) {
		t.Fatalf("invalid SASL mechanism must be static configuration error: %v", err)
	}
	t.Setenv("VEGA_AUDIT_KAFKA_SASL_MECHANISM", "PLAIN")
	t.Setenv("VEGA_AUDIT_ENVIRONMENT", "invalid")
	_, err = NewKafkaRuntimeFromEnv(NewPublishTelemetry())
	if !errors.Is(err, ErrInvalidKafkaConfiguration) {
		t.Fatalf("invalid environment must be static configuration error: %v", err)
	}
}
