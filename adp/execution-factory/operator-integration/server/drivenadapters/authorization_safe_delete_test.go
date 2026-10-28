package drivenadapters

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSafePolicyDeletionHasIndependentBudget(t *testing.T) {
	t.Setenv("BKN_SAFE_DELETE_TIMEOUT", "500ms")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(40 * time.Millisecond):
			w.WriteHeader(http.StatusNoContent)
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	client := newSafeAuthorization(server.URL, nil)
	client.http.Timeout = 10 * time.Millisecond
	if err := client.del(t.Context(), "/api/safe/v1/authz/policies", nil); err != nil {
		t.Fatalf("deletion used the ordinary read budget: %v", err)
	}
	if err := client.post(t.Context(), "/api/safe/v1/authz/checks", nil, nil); err == nil {
		t.Fatal("ordinary requests unexpectedly received the deletion budget")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := client.del(ctx, "/api/safe/v1/authz/policies", nil); err == nil {
		t.Fatal("delete ignored caller cancellation")
	}
	t.Setenv("BKN_SAFE_DELETE_TIMEOUT", "10ms")
	if err := client.del(t.Context(), "/api/safe/v1/authz/policies", nil); err == nil {
		t.Fatal("delete ignored the configured timeout")
	}
	if client.http.Timeout != 10*time.Millisecond {
		t.Fatal("delete mutated the shared client")
	}
}
