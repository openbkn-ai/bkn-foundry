// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package otellog

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestBaseLogAttributes(t *testing.T) {
	previousServiceName := globalServiceName
	t.Cleanup(func() {
		globalServiceName = previousServiceName
	})
	SetServiceName("test-service")

	attrs := baseLogAttributes(trace.SpanFromContext(context.Background()))

	if len(attrs) != 1 {
		t.Fatalf("expected exactly one base attribute, got %d", len(attrs))
	}
	if attrs[0].Key != "service.name" || attrs[0].Value.AsString() != "test-service" {
		t.Fatalf("unexpected base attribute: %s=%s", attrs[0].Key, attrs[0].Value.AsString())
	}
}
