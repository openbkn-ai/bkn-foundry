package permission

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

func TestSafeDeletionHasIndependentPerResourceBudget(t *testing.T) {
	t.Setenv("BKN_SAFE_DELETE_TIMEOUT", "500ms")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(40 * time.Millisecond):
			w.WriteHeader(http.StatusNoContent)
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	client := newSafeClient(server.URL)
	client.http.Timeout = 10 * time.Millisecond
	access := &safePermissionAccess{safe: client}
	if err := access.DeleteResources(t.Context(), []interfaces.PermissionResource{
		{Type: "function", ID: "f1"}, {Type: "skill", ID: "s1"},
	}); err != nil {
		t.Fatalf("policy deletion used the ordinary read budget: %v", err)
	}
	if err := client.do(t.Context(), http.MethodPost, "/api/safe/v1/authz/checks", nil, nil); err == nil {
		t.Fatal("ordinary requests unexpectedly received the deletion budget")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := access.DeleteResources(ctx, []interfaces.PermissionResource{{Type: "function", ID: "f1"}}); err == nil {
		t.Fatal("delete ignored caller cancellation")
	}
	t.Setenv("BKN_SAFE_DELETE_TIMEOUT", "10ms")
	if err := access.DeleteResources(t.Context(), []interfaces.PermissionResource{{Type: "function", ID: "f1"}}); err == nil {
		t.Fatal("delete ignored the configured timeout")
	}
	if client.http.Timeout != 10*time.Millisecond {
		t.Fatal("delete mutated the shared client")
	}
}
