// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package config

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
		},
		Hydra: HydraConfig{
			AdminURL:  "http://127.0.0.1:4445",
			PublicURL: "http://127.0.0.1:4444",
		},
		LDAP: LDAPConfig{
			UserFilter: "(uid=%s)",
		},
	}
}
