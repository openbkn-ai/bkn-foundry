// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package logger

import (
	"log"
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"

	"github.com/openbkn-ai/bkn-foundry/comm-go/common"
)

type LogSetting struct {
	LogServiceName string `json:"logServiceName" mapstructure:"logServiceName"`
	LogFileName    string `json:"logFileName"    mapstructure:"logFileName"`
	LogLevel       string `json:"logLevel"       mapstructure:"logLevel"`
	DevelopMode    bool   `json:"developMode"    mapstructure:"developMode"`
	MaxAge         int    `json:"maxAge"         mapstructure:"maxAge"`
	MaxBackups     int    `json:"maxBackups"     mapstructure:"maxBackups"`
	MaxSize        int    `json:"maxSize"        mapstructure:"maxSize"`
}

// InitLogger initializes the logger.
func InitLogger(setting LogSetting) *zap.SugaredLogger {
	level, err := zapcore.ParseLevel(setting.LogLevel)
	if err != nil {
		log.Fatalf("Parse Log Level failed:%s", setting.LogLevel)
	}

	ws := []zapcore.WriteSyncer{
		zapcore.AddSync(os.Stdout),
	}

	if setting.LogFileName != "" {
		// File output hook.
		hook := &lumberjack.Logger{
			Filename:   setting.LogFileName,
			LocalTime:  true,               // Use local time in log file names.
			MaxAge:     setting.MaxAge,     // Maximum retention period in days.
			MaxBackups: setting.MaxBackups, // Maximum number of retained files.
			MaxSize:    setting.MaxSize,    // Maximum size of a single file in MB.
		}
		ws = append(ws, zapcore.AddSync(hook))
	}

	// Log encoder configuration.
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "time",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,                    // Lowercase level encoder.
		EncodeTime:     zapcore.TimeEncoderOfLayout(common.RFC3339Milli), // RFC3339Milli timestamp format.
		EncodeDuration: zapcore.SecondsDurationEncoder,                   //
		EncodeCaller:   zapcore.FullCallerEncoder,                        // Full caller-path encoder.
		EncodeName:     zapcore.FullNameEncoder,
	}

	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderConfig), // Encoder configuration.
		zapcore.NewMultiWriteSyncer(ws...),    // Write to stdout and files.
		level,                                 // Log level.
	)

	options := []zap.Option{
		zap.Fields(zap.String("serviceName", setting.LogServiceName)),
	}

	if setting.DevelopMode {
		// Enable development-mode stack traces.
		options = append(options, zap.AddCaller(), zap.AddCallerSkip(1), zap.Development())
	}

	logger := zap.New(core, options...).Sugar()
	return logger
}
