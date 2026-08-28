// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package telemetry Observability related packages.
package telemetry

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	otellogapi "go.opentelemetry.io/otel/log"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// LogExporterType log export type.
type LogExporterType string

const (
	LogExporterTypeConsole LogExporterType = "console" // Console export.
	LogExporterTypeOTLP    LogExporterType = "http"    // http export.
	contextLoaderLogSource                 = "context-loader"
)

// SamplerLogger samplinglogger.
type SamplerLogger struct {
	DefaultLogger interfaces.Logger
}

// NewSamplerLogger createlogger.
func NewSamplerLogger(defaultLogger interfaces.Logger) interfaces.Logger {
	s := &SamplerLogger{
		DefaultLogger: defaultLogger,
	}
	return s
}

func (l *SamplerLogger) Debug(v ...interface{}) {
	s := &spanLogger{
		maker: l,
	}
	s.Debug(v...)
}

func (l *SamplerLogger) Info(v ...interface{}) {
	s := &spanLogger{
		maker: l,
	}
	s.Info(v...)
}

func (l *SamplerLogger) Warn(v ...interface{}) {
	s := &spanLogger{
		maker: l,
	}
	s.Warn(v...)
}

func (l *SamplerLogger) Error(v ...interface{}) {
	s := &spanLogger{
		maker: l,
	}
	s.Error(v...)
}

func (l *SamplerLogger) Debugf(format string, v ...interface{}) {
	s := &spanLogger{
		maker: l,
	}
	s.Debugf(format, v...)
}

func (l *SamplerLogger) Infof(format string, v ...interface{}) {
	s := &spanLogger{
		maker: l,
	}
	s.Infof(format, v...)
}

func (l *SamplerLogger) Warnf(format string, v ...interface{}) {
	s := &spanLogger{
		maker: l,
	}
	s.Warnf(format, v...)
}

func (l *SamplerLogger) Errorf(format string, v ...interface{}) {
	s := &spanLogger{
		maker: l,
	}
	s.Errorf(format, v...)
}

// WithContext passes context.
func (l *SamplerLogger) WithContext(ctx context.Context) interfaces.Logger {
	return &spanLogger{
		ctx:   ctx,
		maker: l,
	}
}

type spanLogger struct {
	ctx   context.Context
	maker *SamplerLogger
}

func (s *spanLogger) Debug(v ...interface{}) {
	localMsg := fmt.Sprint(v...)
	s.maker.DefaultLogger.Debug(localMsg)
	msg := internalLogMessage(s.ctx, localMsg)
	if s.ctx != nil {
		otellog.LogDebug(s.ctx, msg, contextLogAttributes(s.ctx)...)
		return
	}
	otellog.LogDebug(context.Background(), msg)
}

func (s *spanLogger) Info(v ...interface{}) {
	localMsg := fmt.Sprint(v...)
	s.maker.DefaultLogger.Info(localMsg)
	msg := internalLogMessage(s.ctx, localMsg)
	if s.ctx != nil {
		otellog.LogInfo(s.ctx, msg, contextLogAttributes(s.ctx)...)
		return
	}
	otellog.LogInfo(context.Background(), msg)
}

func (s *spanLogger) Warn(v ...interface{}) {
	localMsg := fmt.Sprint(v...)
	s.maker.DefaultLogger.Warn(localMsg)
	msg := internalLogMessage(s.ctx, localMsg)
	if s.ctx != nil {
		otellog.LogWarn(s.ctx, msg, contextLogAttributes(s.ctx)...)
		return
	}
	otellog.LogWarn(context.Background(), msg)
}

func (s *spanLogger) Error(v ...interface{}) {
	localMsg := fmt.Sprint(v...)
	s.maker.DefaultLogger.Error(localMsg)
	msg := internalLogMessage(s.ctx, localMsg)
	if s.ctx != nil {
		otellog.LogError(s.ctx, msg, nil, contextLogAttributes(s.ctx)...)
		return
	}
	otellog.LogError(context.Background(), msg, nil)
}

func (s *spanLogger) Debugf(format string, v ...interface{}) {
	localMsg := fmt.Sprintf(format, v...)
	s.maker.DefaultLogger.Debug(localMsg)
	msg := internalLogMessage(s.ctx, localMsg)
	if s.ctx != nil {
		otellog.LogDebug(s.ctx, msg, contextLogAttributes(s.ctx)...)
		return
	}
	otellog.LogDebug(context.Background(), msg)
}

func (s *spanLogger) Infof(format string, v ...interface{}) {
	localMsg := fmt.Sprintf(format, v...)
	s.maker.DefaultLogger.Info(localMsg)
	msg := internalLogMessage(s.ctx, localMsg)
	if s.ctx != nil {
		otellog.LogInfo(s.ctx, msg, contextLogAttributes(s.ctx)...)
		return
	}
	otellog.LogInfo(context.Background(), msg)
}

func (s *spanLogger) Warnf(format string, v ...interface{}) {
	localMsg := fmt.Sprintf(format, v...)
	s.maker.DefaultLogger.Warn(localMsg)
	msg := internalLogMessage(s.ctx, localMsg)
	if s.ctx != nil {
		otellog.LogWarn(s.ctx, msg, contextLogAttributes(s.ctx)...)
		return
	}
	otellog.LogWarn(context.Background(), msg)
}

func (s *spanLogger) Errorf(format string, v ...interface{}) {
	localMsg := fmt.Sprintf(format, v...)
	s.maker.DefaultLogger.Error(localMsg)
	msg := internalLogMessage(s.ctx, localMsg)
	if s.ctx != nil {
		otellog.LogError(s.ctx, msg, nil, contextLogAttributes(s.ctx)...)
		return
	}
	otellog.LogError(context.Background(), msg, nil)
}

func (s *spanLogger) WithContext(ctx context.Context) interfaces.Logger {
	s.ctx = ctx
	return s
}

func contextLogAttributes(ctx context.Context) []otellogapi.KeyValue {
	attrs := []otellogapi.KeyValue{otellogapi.String("schema_version", "1.0.0")}
	traceContext, hasTraceContext := common.GetTraceContextFromCtx(ctx)
	if hasTraceContext {
		attrs = appendNonEmptyLogAttribute(attrs, "tenant_id", traceContext.TenantID)
		attrs = appendNonEmptyLogAttribute(attrs, "request_id", traceContext.RequestID)
		attrs = appendNonEmptyLogAttribute(attrs, "conversation_id", traceContext.ConversationID)
		attrs = appendNonEmptyLogAttribute(attrs, "interaction_id", traceContext.InteractionID)
		attrs = appendNonEmptyLogAttribute(attrs, "operation_id", traceContext.OperationID)
		attrs = appendNonEmptyLogAttribute(attrs, "tool_name", traceContext.ToolName)
	}
	if authContext, ok := common.GetAccountAuthContextFromCtx(ctx); ok && authContext != nil {
		attrs = appendNonEmptyLogAttribute(attrs, "actor_id", authContext.AccountID)
		if authContext.AccountType == interfaces.AccessorTypeUser {
			attrs = appendNonEmptyLogAttribute(attrs, "effective_subject_id", authContext.AccountID)
		} else {
			attrs = appendNonEmptyLogAttribute(attrs, "application_id", authContext.AccountID)
		}
		if authContext.TokenInfo != nil {
			attrs = appendNonEmptyLogAttribute(attrs, "application_id", authContext.TokenInfo.ClientID)
		}
	}
	return attrs
}

func operationLogAttributes(ctx context.Context, failed bool, sourceLogID string) []otellogapi.KeyValue {
	attrs := contextLogAttributes(ctx)
	attrs = append(attrs,
		otellogapi.String("log_id", contextLoaderLogSource+":"+sourceLogID),
		otellogapi.String("source_id", contextLoaderLogSource),
		otellogapi.String("source_log_id", sourceLogID),
		otellogapi.String("trust_level", "trusted"),
		otellogapi.String("log_category", "runtime.business"),
		otellogapi.String("ingress_principal", contextLoaderLogSource),
	)
	if failed {
		return append(attrs,
			otellogapi.String("event_name", "operation.failed"),
			otellogapi.String("outcome", "failure"),
			otellogapi.String("safe_summary", "OpenBKN operation failed"),
		)
	}
	return append(attrs,
		otellogapi.String("event_name", "operation.completed"),
		otellogapi.String("outcome", "success"),
		otellogapi.String("safe_summary", "OpenBKN operation completed"),
	)
}

// EmitOperationOutcome writes the single governed log record for a durably finalized operation.
func EmitOperationOutcome(ctx context.Context, failed bool) {
	sourceLogID := newSourceLogID()
	attrs := operationLogAttributes(ctx, failed, sourceLogID)
	if failed {
		otellog.LogError(ctx, "OpenBKN operation failed", nil, attrs...)
		return
	}
	otellog.LogInfo(ctx, "OpenBKN operation completed", attrs...)
}

func internalLogMessage(ctx context.Context, original string) string {
	if ctx == nil {
		return original
	}
	traceContext, ok := common.GetTraceContextFromCtx(ctx)
	if ok && traceContext.OperationID != "" {
		return "OpenBKN operation internal detail omitted"
	}
	return original
}

func newSourceLogID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err == nil {
		return "log_" + hex.EncodeToString(value[:])
	}
	return fmt.Sprintf("log_fallback_%d", time.Now().UnixNano())
}

func appendNonEmptyLogAttribute(attrs []otellogapi.KeyValue, key, value string) []otellogapi.KeyValue {
	if value == "" {
		return attrs
	}
	return append(attrs, otellogapi.String(key, value))
}
