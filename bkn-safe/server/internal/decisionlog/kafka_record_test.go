package decisionlog

import "testing"

func TestIsSafeActorIDAcceptsOnlyStableActorIdentifiers(t *testing.T) {
	for _, value := range []string{"user-1", " bkn_valid_accessor_123 "} {
		if !IsSafeActorID(value) {
			t.Fatalf("expected %q to be safe", value)
		}
	}
	for _, value := range []string{"", "redacted", "Bearer abcdefghijklmnop", "user id"} {
		if IsSafeActorID(value) {
			t.Fatalf("expected %q to be rejected", value)
		}
	}
}
