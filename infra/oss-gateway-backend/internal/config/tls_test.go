package config

import "testing"

func TestOutboundCABundleEnvironment(t *testing.T) {
	t.Setenv("OUTBOUND_TLS_CA_FILE", "/test/private-ca.pem")
	cfg := NewConfig()
	if cfg.OutboundTLS.CAFile != "/test/private-ca.pem" {
		t.Fatalf("CA file was not loaded: %q", cfg.OutboundTLS.CAFile)
	}
}
