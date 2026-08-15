// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package otel

import (
	"context"
	"log"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
)

// InitOTel initializes trace and log providers.
// Call it once at startup and call providers.Shutdown(ctx) during shutdown.
func InitOTel(ctx context.Context, cfg *OtelConfig) (*Providers, error) {
	cfg.SetDefaults(cfg.ServiceName, cfg.ServiceVersion)
	otellog.SetServiceName(cfg.ServiceName)

	res, err := buildResource(ctx, cfg)
	if err != nil {
		return nil, err
	}

	providers := &Providers{}

	// Initialize the trace provider.
	if cfg.Trace.Enabled {
		tracerProvider, err := newTracerProvider(ctx, cfg.OTLPEndpoint, cfg.Trace.SamplingRate, res)
		if err != nil {
			return nil, err
		}

		otel.SetTracerProvider(tracerProvider)
		providers.TracerProvider = tracerProvider
	}

	// Configure the global propagator.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	// Initialize the log provider.
	if cfg.Log.Enabled {
		loggerProvider, err := newLoggerProvider(ctx, cfg.OTLPEndpoint, res)
		if err != nil {
			return nil, err
		}

		global.SetLoggerProvider(loggerProvider)
		providers.LoggerProvider = loggerProvider
	}

	log.Printf("[OTel] Initialized for service=%s, endpoint=%s (HTTP), trace=%v, log=%v",
		cfg.ServiceName, cfg.OTLPEndpoint, cfg.Trace.Enabled, cfg.Log.Enabled)

	return providers, nil
}
