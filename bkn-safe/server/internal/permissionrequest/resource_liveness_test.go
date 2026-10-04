package permissionrequest

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/config"
)

func TestResourceLivenessEscapesResourceIDAsOnePathSegment(t *testing.T) {
	const resourceID = "resource/../?redirect=https://example.invalid"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := "/api/bkn-backend/in/v1/knowledge-networks/" + url.PathEscape(resourceID)
		if r.URL.EscapedPath() != want {
			t.Fatalf("escaped path = %q, want %q", r.URL.EscapedPath(), want)
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	upstream := config.UpstreamConfig{BaseURL: server.URL, Timeout: time.Second}
	resolver, err := NewHTTPResourceLivenessResolver(upstream, upstream, upstream)
	if err != nil {
		t.Fatal(err)
	}
	if exists, err := resolver.Exists(t.Context(), "knowledge_network", resourceID); err != nil || exists {
		t.Fatalf("Exists() = (%v, %v), want (false, nil)", exists, err)
	}
}

func TestResourceLivenessDoesNotFollowUpstreamRedirects(t *testing.T) {
	redirectFollowed := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/private" {
			redirectFollowed = true
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(w, r, "/private", http.StatusFound)
	}))
	defer server.Close()

	upstream := config.UpstreamConfig{BaseURL: server.URL, Timeout: time.Second}
	resolver, err := NewHTTPResourceLivenessResolver(upstream, upstream, upstream)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Exists(t.Context(), "knowledge_network", "resource"); err == nil {
		t.Fatal("redirect response was accepted")
	}
	if redirectFollowed {
		t.Fatal("liveness client followed an upstream redirect")
	}
}
