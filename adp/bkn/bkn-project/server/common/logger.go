// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package common

import (
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/version"
)

const (
	// Log storage location.
	logFileName = "/opt/bkn-project/logs/bkn-project.log"
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
