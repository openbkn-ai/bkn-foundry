// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package otel

// OtelConfig configures the OpenTelemetry Collector.
type OtelConfig struct {
	ServiceName    string    `yaml:"service_name" mapstructure:"service_name"`
	ServiceVersion string    `yaml:"service_version" mapstructure:"service_version"`
	Environment    string    `yaml:"environment" mapstructure:"environment"`
	OTLPEndpoint   string    `yaml:"otlp_endpoint" mapstructure:"otlp_endpoint"` // e.g. "otel-collector:4318"
	Trace          TraceConf `yaml:"trace" mapstructure:"trace"`
	Log            LogConf   `yaml:"log" mapstructure:"log"`
}

// TraceConf contains trace settings.
type TraceConf struct {
	Enabled      bool    `yaml:"enabled" mapstructure:"enabled"`
	SamplingRate float64 `yaml:"sampling_rate" mapstructure:"sampling_rate"`
}

// LogConf contains log settings.
type LogConf struct {
	Enabled bool   `yaml:"enabled" mapstructure:"enabled"`
	Level   string `yaml:"level" mapstructure:"level"`
}

// SetDefaults applies default OtelConfig values.
func (c *OtelConfig) SetDefaults(serverName string, serverVersion string) {
	if c.ServiceName == "" {
		c.ServiceName = serverName
	}

	if c.ServiceVersion == "" {
		c.ServiceVersion = serverVersion
	}

	if c.Environment == "" {
		c.Environment = "production"
	}

	if c.OTLPEndpoint == "" {
		c.OTLPEndpoint = "localhost:4318"
	}

	if c.Trace.SamplingRate <= 0 || c.Trace.SamplingRate > 1 {
		c.Trace.SamplingRate = 1.0
	}

	if c.Log.Level == "" {
		c.Log.Level = "info"
	}
}
