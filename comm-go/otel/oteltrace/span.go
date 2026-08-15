// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package oteltrace

import (
	"context"
	"fmt"
	"net/http"
	"runtime"
	"strings"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
	attr "go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const (
	// InstrumentationName is the instrumentation name used to create tracers.
	InstrumentationName = "bkn-backend/otel"
)

// StartInternalSpan starts a span for an in-service function call and derives its name from runtime.Caller.
func StartInternalSpan(ctx context.Context) (context.Context, trace.Span) {
	name, filepath := callerFuncName(2)
	newCtx, span := StartNamedInternalSpan(ctx, name)
	if filepath != "" {
		span.SetAttributes(attr.String("code.filepath", filepath))
	}
	return newCtx, span
}

// StartClientSpan starts a span for an external dependency call and derives its name from runtime.Caller.
func StartClientSpan(ctx context.Context) (context.Context, trace.Span) {
	name, filepath := callerFuncName(2)
	newCtx, span := StartNamedClientSpan(ctx, name)
	if filepath != "" {
		span.SetAttributes(attr.String("code.filepath", filepath))
	}
	return newCtx, span
}

// StartServerSpanFromContext starts a server span and derives its name from runtime.Caller.
func StartServerSpanFromContext(ctx context.Context) (context.Context, trace.Span) {
	name, filepath := callerFuncName(2)
	newCtx, span := StartNamedServerSpan(ctx, name)
	if filepath != "" {
		span.SetAttributes(attr.String("code.filepath", filepath))
	}
	return newCtx, span
}

// StartProducerSpan starts a message producer span and derives its name from runtime.Caller.
func StartProducerSpan(ctx context.Context) (context.Context, trace.Span) {
	name, filepath := callerFuncName(2)
	newCtx, span := StartNamedProducerSpan(ctx, name)
	if filepath != "" {
		span.SetAttributes(attr.String("code.filepath", filepath))
	}
	return newCtx, span
}

// StartConsumerSpan starts a message consumer span and derives its name from runtime.Caller.
func StartConsumerSpan(ctx context.Context) (context.Context, trace.Span) {
	name, filepath := callerFuncName(2)
	newCtx, span := StartNamedConsumerSpan(ctx, name)
	if filepath != "" {
		span.SetAttributes(attr.String("code.filepath", filepath))
	}
	return newCtx, span
}

func callerFuncName(skip int) (string, string) {
	pc, file, lineNo, ok := runtime.Caller(skip)
	if !ok {
		return "unknown", ""
	}
	funcPaths := strings.Split(runtime.FuncForPC(pc).Name(), "/")
	return funcPaths[len(funcPaths)-1], fmt.Sprintf("%s:%v", file, lineNo)
}

// StartNamedClientSpan starts a SpanKindClient span with a caller-provided name.
func StartNamedClientSpan(ctx context.Context, name string) (context.Context, trace.Span) {
	return otel.Tracer(InstrumentationName).Start(ctx, name, trace.WithSpanKind(trace.SpanKindClient))
}

// StartNamedInternalSpan starts a SpanKindInternal span with a caller-provided name.
func StartNamedInternalSpan(ctx context.Context, name string) (context.Context, trace.Span) {
	return otel.Tracer(InstrumentationName).Start(ctx, name, trace.WithSpanKind(trace.SpanKindInternal))
}

// StartNamedServerSpan starts a SpanKindServer span with a caller-provided name.
func StartNamedServerSpan(ctx context.Context, name string) (context.Context, trace.Span) {
	return otel.Tracer(InstrumentationName).Start(ctx, name, trace.WithSpanKind(trace.SpanKindServer))
}

// StartNamedProducerSpan starts a SpanKindProducer span with a caller-provided name.
func StartNamedProducerSpan(ctx context.Context, name string) (context.Context, trace.Span) {
	return otel.Tracer(InstrumentationName).Start(ctx, name, trace.WithSpanKind(trace.SpanKindProducer))
}

// StartNamedConsumerSpan starts a SpanKindConsumer span with a caller-provided name.
func StartNamedConsumerSpan(ctx context.Context, name string) (context.Context, trace.Span) {
	return otel.Tracer(InstrumentationName).Start(ctx, name, trace.WithSpanKind(trace.SpanKindConsumer))
}

// StartServerSpan starts a span for a cross-service HTTP request.
// Prerequisites: TracingMiddleware extracts trace headers into c.Request.Context(),
// and LanguageMiddleware adds the language to c.Request.Context().
func StartServerSpan(c *gin.Context) (context.Context, trace.Span) {
	spanName := fmt.Sprintf("%s %s", c.Request.Method, c.FullPath())
	newCtx, span := StartNamedServerSpan(c.Request.Context(), spanName)
	span.SetAttributes(
		attr.String("http.request.method", c.Request.Method),
		attr.String("http.route", c.FullPath()),
		attr.String("client.address", c.ClientIP()),
	)

	return newCtx, span
}

// ExtractTraceHeader extracts trace context from HTTP headers.
func ExtractTraceHeader(ctx context.Context, header http.Header) context.Context {
	if header == nil {
		return ctx
	}

	return otel.GetTextMapPropagator().Extract(ctx, propagation.HeaderCarrier(header))
}

// SetAttributes applies attributes to the current span.
func SetAttributes(ctx context.Context, kv ...attr.KeyValue) {
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(kv...)
}

// EndSpan finishes the current span and records an error when provided.
func EndSpan(ctx context.Context, err error) {
	span := trace.SpanFromContext(ctx)
	if span == nil {
		return
	}

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else {
		span.SetStatus(codes.Ok, "OK")
	}

	span.End()
}
