package conf

import "testing"

func TestTraceReleaseConfigHasManagedDefaults(t *testing.T) {
	t.Setenv("BKN_TRACE_RELEASE_STATE_PATH", "")
	t.Setenv("BKN_TRACE_RELEASE_MANIFEST_PATH", "")
	t.Setenv("BKN_TRACE_RELEASE_NAMESPACE", "")
	config := NewTraceReleaseConfig()
	if config.StatePath != "/var/lib/openbkn/trace-evidence/state.json" {
		t.Fatalf("unexpected state path: %s", config.StatePath)
	}
	if config.ManifestPath != "/etc/openbkn/trace-evidence/services.yaml" || config.Namespace != "openbkn" {
		t.Fatalf("unexpected managed defaults: %+v", config)
	}
}
