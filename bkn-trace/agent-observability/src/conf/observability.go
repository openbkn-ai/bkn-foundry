package conf

import "os"

type ObservabilityConfig struct {
	CursorSigningKey []byte
}

func NewObservabilityConfig() ObservabilityConfig {
	return ObservabilityConfig{
		CursorSigningKey: []byte(os.Getenv("BKN_OBSERVABILITY_CURSOR_SIGNING_KEY")),
	}
}
