package main

import "testing"

func TestEnvEnabledHonorsIndependentSwitches(t *testing.T) {
	t.Setenv("TRACE_ENABLED", "false")
	t.Setenv("LOG_ENABLED", "true")
	if envEnabled("TRACE_ENABLED", true) {
		t.Fatal("trace switch was ignored")
	}
	if !envEnabled("LOG_ENABLED", false) {
		t.Fatal("log switch was ignored")
	}
}

func TestEnvEnabledFallsBackForMissingOrInvalidValue(t *testing.T) {
	t.Setenv("TRACE_ENABLED", "invalid")
	if !envEnabled("TRACE_ENABLED", true) {
		t.Fatal("invalid value must use bounded default")
	}
	if envEnabled("MISSING_OBSERVABILITY_SWITCH", false) {
		t.Fatal("missing value must use bounded default")
	}
}
