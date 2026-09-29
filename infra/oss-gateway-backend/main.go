package main

import (
	"context"
	"os"
	"oss-gateway/internal/config"
	"oss-gateway/internal/database"
	"oss-gateway/internal/logger"
	"oss-gateway/internal/server"
	"oss-gateway/pkg/crypto"
	"strconv"

	_ "github.com/joho/godotenv/autoload"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel"
)

func main() {
	cfg := config.NewConfig()
	log := logger.NewLogger(cfg)
	providers, err := otel.InitOTel(context.Background(), &otel.OtelConfig{
		ServiceName: "oss-gateway-backend", ServiceVersion: "0.2.0",
		Environment: os.Getenv("ENVIRONMENT"), OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		Trace: otel.TraceConf{Enabled: envEnabled("TRACE_ENABLED", true), SamplingRate: 1},
		Log:   otel.LogConf{Enabled: envEnabled("LOG_ENABLED", true), Level: "info"},
	})
	if err != nil {
		log.WithError(err).Warn("OTLP observability unavailable; business continues")
	} else {
		defer providers.Shutdown(context.Background())
	}
	aesCrypto, err := crypto.NewAESCrypto(cfg.CryptoConfig.AESKey)
	if err != nil {
		log.WithError(err).Fatal("Failed to initialize AES crypto")
	}

	db := database.NewGorm(cfg, log.WithField("module", "database"))

	srv := server.NewServer(cfg, log, db, aesCrypto)
	srv.Start()
}

func envEnabled(name string, fallback bool) bool {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	enabled, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return enabled
}
