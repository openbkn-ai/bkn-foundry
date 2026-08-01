package conf

import (
	"testing"
	"time"
)

func TestNewAccessScopeConfigUsesFailClosedSafeDefaults(t *testing.T) {
	t.Setenv("BKN_SAFE_BASE_URL", "")
	t.Setenv("BKN_SAFE_ACCESS_TIMEOUT", "")
	config := NewAccessScopeConfig()
	if config.BKNBaseURL != "http://bkn-safe:3000" || config.Timeout != 3*time.Second {
		t.Fatalf("unexpected defaults: %+v", config)
	}
}

func TestNewAccessScopeConfigReadsSafeEndpointAndTimeout(t *testing.T) {
	t.Setenv("BKN_SAFE_BASE_URL", "http://safe.internal:3100/")
	t.Setenv("BKN_SAFE_ACCESS_TIMEOUT", "750ms")
	config := NewAccessScopeConfig()
	if config.BKNBaseURL != "http://safe.internal:3100" || config.Timeout != 750*time.Millisecond {
		t.Fatalf("unexpected configured values: %+v", config)
	}
}
