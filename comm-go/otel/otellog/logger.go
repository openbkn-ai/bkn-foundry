// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package otellog

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"go.opentelemetry.io/otel/codes"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/trace"
)

// globalServiceName is set by InitOTel.
var globalServiceName string

// SetServiceName sets the global service name.
func SetServiceName(name string) {
	globalServiceName = name
}

// LogDebug emits a Debug-level structured log with trace context and writes to zap stdout.
func LogDebug(ctx context.Context, message string, attrs ...otellog.KeyValue) {
	emitLog(ctx, otellog.SeverityDebug, message, attrs...)
	logger.Debug(formatForStdout(ctx, message, attrs))
}

// LogInfo emits an Info-level structured log with trace context and writes to zap stdout.
func LogInfo(ctx context.Context, message string, attrs ...otellog.KeyValue) {
	emitLog(ctx, otellog.SeverityInfo, message, attrs...)
	logger.Info(formatForStdout(ctx, message, attrs))
}

// LogWarn emits a Warn-level structured log with trace context and writes to zap stdout.
func LogWarn(ctx context.Context, message string, attrs ...otellog.KeyValue) {
	emitLog(ctx, otellog.SeverityWarn, message, attrs...)
	logger.Warn(formatForStdout(ctx, message, attrs))
}

// LogError emits an Error-level structured log, records it on the current span, and writes to zap stdout.
func LogError(ctx context.Context, message string, err error, attrs ...otellog.KeyValue) {
	span := trace.SpanFromContext(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, message)
	}

	allAttrs := baseLogAttributes(span)
	if err != nil {
		allAttrs = append(allAttrs, otellog.String("error.message", err.Error()))
	}
	allAttrs = append(allAttrs, attrs...)

	otelLogger := global.GetLoggerProvider().Logger(globalServiceName)

	record := otellog.Record{}
	record.SetTimestamp(time.Now())
	record.SetSeverity(otellog.SeverityError)
	record.SetSeverityText("ERROR")
	record.SetBody(otellog.StringValue(message))
	record.AddAttributes(allAttrs...)
	otelLogger.Emit(ctx, record)

	logger.Error(formatForStdout(ctx, message, allAttrs))
}

// emitLog sends an OpenTelemetry log record.
func emitLog(ctx context.Context, severity otellog.Severity, message string, attrs ...otellog.KeyValue) {
	span := trace.SpanFromContext(ctx)

	allAttrs := baseLogAttributes(span)
	allAttrs = append(allAttrs, attrs...)

	otelLogger := global.GetLoggerProvider().Logger(globalServiceName)

	record := otellog.Record{}
	record.SetTimestamp(time.Now())
	record.SetSeverity(severity)
	record.SetSeverityText(severity.String())
	record.SetBody(otellog.StringValue(message))
	record.AddAttributes(allAttrs...)
	otelLogger.Emit(ctx, record)
}

// baseLogAttributes builds base log attributes, including trace correlation metadata.
func baseLogAttributes(span trace.Span) []otellog.KeyValue {
	attrs := []otellog.KeyValue{
		otellog.String("service.name", globalServiceName),
	}

	spanCtx := span.SpanContext()
	if spanCtx.HasTraceID() {
		attrs = append(attrs, otellog.String("trace_id", spanCtx.TraceID().String()))
	}

	if spanCtx.HasSpanID() {
		attrs = append(attrs, otellog.String("span_id", spanCtx.SpanID().String()))
	}

	return attrs
}

// formatForStdout formats a stdout log line: [trace=... span=...] message k=v k=v.
func formatForStdout(ctx context.Context, message string, attrs []otellog.KeyValue) string {
	var b strings.Builder
	sc := trace.SpanFromContext(ctx).SpanContext()
	if sc.HasTraceID() {
		fmt.Fprintf(&b, "[trace=%s span=%s] ", sc.TraceID(), sc.SpanID())
	}
	b.WriteString(message)
	for _, a := range attrs {
		fmt.Fprintf(&b, " %s=%s", a.Key, a.Value.String())
	}
	return b.String()
}
