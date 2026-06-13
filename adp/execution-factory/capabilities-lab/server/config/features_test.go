package config

import "testing"

func TestEnvBoolDefaultsTrue(t *testing.T) {
	t.Setenv("LAB_FEATURE_CATALOG", "")

	flags := LoadFeatureFlags()
	if !flags.Catalog {
		t.Fatalf("expected catalog enabled by default")
	}
}

func TestEnvBoolExplicitFalse(t *testing.T) {
	t.Setenv("LAB_FEATURE_CATALOG", "false")

	flags := LoadFeatureFlags()
	if flags.Catalog {
		t.Fatalf("expected catalog disabled")
	}
}
