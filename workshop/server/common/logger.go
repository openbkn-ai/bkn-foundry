// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package common

import (
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"github.com/openbkn-ai/bkn-foundry/workshop/server/version"
)

const logFileName = "/opt/workshop/logs/workshop.log"

func init() {
	logger.InitGlobalLogger(logger.LogSetting{
		LogServiceName: version.ServerName,
		LogFileName:    logFileName,
		LogLevel:       "info",
		DevelopMode:    false,
		MaxAge:         100,
		MaxBackups:     20,
		MaxSize:        100,
	})
}

// SetLogSetting applies the configured log settings.
func SetLogSetting(setting logger.LogSetting) {
	setting.LogServiceName = version.ServerName
	setting.LogFileName = logFileName
	logger.InitGlobalLogger(setting)
}
