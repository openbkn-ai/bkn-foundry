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

	"github.com/openbkn-ai/bkn-comm-go/common"
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

// 初始化日志对象
func InitLogger(setting LogSetting) *zap.SugaredLogger {
	level, err := zapcore.ParseLevel(setting.LogLevel)
	if err != nil {
		log.Fatalf("Parse Log Level failed:%s", setting.LogLevel)
	}

	ws := []zapcore.WriteSyncer{
		zapcore.AddSync(os.Stdout),
	}

	if setting.LogFileName != "" {
		// 日志文件hook
		hook := &lumberjack.Logger{
			Filename:   setting.LogFileName,
			LocalTime:  true,               //日志文件名的时间格式为本地时间
			MaxAge:     setting.MaxAge,     //文件保留的最长时间，单位为天
			MaxBackups: setting.MaxBackups, // 旧文件保留的最大个数
			MaxSize:    setting.MaxSize,    // 单个文件最大长度，单位是M
		}
		ws = append(ws, zapcore.AddSync(hook))
	}

	// 日志格式设定
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "time",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,                    // 小写编码器
		EncodeTime:     zapcore.TimeEncoderOfLayout(common.RFC3339Milli), // RFC3339Milli 时间格式
		EncodeDuration: zapcore.SecondsDurationEncoder,                   //
		EncodeCaller:   zapcore.FullCallerEncoder,                        // 全路径编码器
		EncodeName:     zapcore.FullNameEncoder,
	}

	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderConfig), // 编码器配置
		zapcore.NewMultiWriteSyncer(ws...),    // 打印到控制台和文件
		level,                                 // 日志级别
	)

	options := []zap.Option{
		zap.Fields(zap.String("serviceName", setting.LogServiceName)),
	}

	if setting.DevelopMode {
		// 开启开发模式，堆栈跟踪
		options = append(options, zap.AddCaller(), zap.AddCallerSkip(1), zap.Development())
	}

	logger := zap.New(core, options...).Sugar()
	return logger
}
