// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package common

import (
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"vega-backend/version"
)

const (
	// Log storage location.
	logFileName = "/opt/vega-backend/logs/vega-backend.log"
)

// GetLogger returns the logger handle.
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
