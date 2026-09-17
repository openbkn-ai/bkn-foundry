// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package config

import "time"

// defaultConfig returns dev-friendly defaults (same as historical env-only Load).
func defaultConfig() *Config {
	return &Config{
		HTTPAddr:    ":3000",
		SeedOnStart: true,
		DB: DBConfig{
			Type:     "MySQL",
			Host:     "127.0.0.1",
			Port:     3306,
			User:     "safe",
			Password: "secret",
			Name:     "safe",

			MaxOpenConns:    50,
			ConnMaxIdleTime: 2 * time.Minute,
			ConnMaxLifetime: 30 * time.Minute,
		},
		Hydra: HydraConfig{
			AdminURL:  "http://127.0.0.1:4445",
			PublicURL: "http://127.0.0.1:4444",
		},
		LDAP: LDAPConfig{
			UserFilter: "(uid=%s)",
		},
		License: LicenseConfig{
			ServerURL: "https://license.openbkn.ai",
		},
		Audit: AuditConfig{
			ChainHeadLogInterval: 15 * time.Minute,
			DecisionLog: DecisionLogConfig{
				Enabled:         true,
				AllowSampleRate: 1,
				QueueSize:       4096,
				RetentionDays:   90,
			},
		},
		Authz: AuthzConfig{
			PolicyRefreshInterval: 10 * time.Minute,
		},
		Upstreams: UpstreamsConfig{
			BKNBackend: UpstreamConfig{
				BaseURL: "http://bkn-backend-svc:13014",
				Timeout: 3 * time.Second,
			},
			ExecutionFactory: UpstreamConfig{
				BaseURL: "http://agent-operator-integration:9000",
				Timeout: 3 * time.Second,
			},
			VegaBackend: UpstreamConfig{
				BaseURL: "http://vega-backend-svc:13014",
				Timeout: 3 * time.Second,
			},
		},
	}
}
