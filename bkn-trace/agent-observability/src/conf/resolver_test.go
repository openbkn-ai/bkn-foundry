package conf

import (
	"testing"
	"time"
)

func TestBusinessResolverConfigIsDisabledWithoutTrustedEndpoints(t *testing.T) {
	t.Setenv("BKN_TRACE_BKN_RESOLVER_URL", "")
	t.Setenv("BKN_TRACE_VEGA_RESOLVER_URL", "")
	config := NewBusinessResolverConfig()
	if config.Enabled || config.Timeout != 3*time.Second {
		t.Fatalf("unexpected default resolver config: %+v", config)
	}
}

func TestBusinessResolverConfigUsesExplicitInternalEndpoints(t *testing.T) {
	t.Setenv("BKN_TRACE_BKN_RESOLVER_URL", "http://bkn-backend:8080/")
	t.Setenv("BKN_TRACE_VEGA_RESOLVER_URL", "http://vega-backend:8080/")
	t.Setenv("BKN_TRACE_RESOLVER_TIMEOUT", "750ms")
	config := NewBusinessResolverConfig()
	if !config.Enabled || config.BKNBaseURL != "http://bkn-backend:8080" || config.VegaBaseURL != "http://vega-backend:8080" || config.Timeout != 750*time.Millisecond {
		t.Fatalf("unexpected resolver config: %+v", config)
	}
}
