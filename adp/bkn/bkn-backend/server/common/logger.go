// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package common

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"

	"bkn-backend/version"
)

const (
	// Log storage location.
	logFileName = "/opt/bkn-backend/logs/bkn-backend.log"
)

// Get the logger handle.
func init() {
	setting := logger.LogSetting{
		LogServiceName: version.ServerName,
		LogFileName:    logFileName,
		LogLevel:       "info",
		DevelopMode:    false,
		MaxAge:         100,
		MaxBackups:     20,
		MaxSize:        100,
	}
	logger.InitGlobalLogger(setting)
}

// SetLogSetting configures logging.
func SetLogSetting(setting logger.LogSetting) {
	setting.LogServiceName = version.ServerName
	setting.LogFileName = logFileName
	logger.InitGlobalLogger(setting)
}

// SafeQuerySummary returns diagnostics that cannot reveal SQL text or argument values.
func SafeQuerySummary(query string, argumentCount int) string {
	return fmt.Sprintf("%s, argument_count=%d", SafeTextSummary("query", query), argumentCount)
}

// SafeTextSummary identifies sensitive text without retaining its contents.
func SafeTextSummary(label, value string) string {
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%s_hash=sha256:%s, %s_length=%d", label, hex.EncodeToString(sum[:]), label, len(value))
}

// SafeErrorSummary keeps error type and identity without logging a dependency response body.
func SafeErrorSummary(err error) string {
	if err == nil {
		return "error=false"
	}
	return fmt.Sprintf("error=true, error_type=%T, %s", err, SafeTextSummary("error", err.Error()))
}

// LogSafeError records an error fingerprint without retaining its message or dependency payload.
func LogSafeError(ctx context.Context, operation string, err error) {
	if err == nil {
		return
	}
	otellog.LogError(ctx, operation+": "+SafeErrorSummary(err), errors.New("redacted error"))
}
