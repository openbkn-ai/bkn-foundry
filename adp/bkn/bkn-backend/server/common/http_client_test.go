package common

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

func TestHTTPClientUsesEffectiveLocale(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(rest.AcceptLanguageHeader); got != rest.AmericanEnglish {
			t.Errorf("Accept-Language = %q, want %q", got, rest.AmericanEnglish)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := rest.NewHTTPClientWithRawClient(server.Client())
	ctx := rest.WithLanguage(context.Background(), rest.AmericanEnglish)
	_, _, err := client.PostNoUnmarshal(ctx, server.URL, map[string]string{
		rest.AcceptLanguageHeader: "zh-CN, en-US;q=0.8",
	}, map[string]string{"id": "resource-1"})
	if err != nil {
		t.Fatalf("PostNoUnmarshal() error = %v", err)
	}
}
