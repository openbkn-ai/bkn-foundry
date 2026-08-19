// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package telemetry

import (
	"context"
	"fmt"

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/attribute"
)

// ExporterType Export type.
type ExporterType string

const (
	ExporterTypeOTLP   ExporterType = "otlp"   // otlp export.
	ExporterTypeJaeger ExporterType = "jaeger" // jaeger export.
)

// SetSpanAttributes sets Span attributes.
func SetSpanAttributes(ctx context.Context, attrs map[string]interface{}) {
	if attrs == nil || ctx == nil {
		return
	}
	attrsList := make([]attribute.KeyValue, 0, len(attrs))
	for k, v := range attrs {
		attrsList = append(attrsList, attribute.String(k, fmt.Sprint(v)))
	}
	oteltrace.SetAttributes(ctx, attrsList...)
}
