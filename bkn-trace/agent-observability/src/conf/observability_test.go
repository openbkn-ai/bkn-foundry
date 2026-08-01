package conf

import "testing"

func TestObservabilityConfigReadsCursorSigningKeyWithoutTransformingIt(t *testing.T) {
	t.Setenv("BKN_OBSERVABILITY_CURSOR_SIGNING_KEY", "local-test-signing-key")
	config := NewObservabilityConfig()
	if string(config.CursorSigningKey) != "local-test-signing-key" {
		t.Fatalf("unexpected cursor signing key")
	}
}
