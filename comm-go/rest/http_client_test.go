// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package rest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPClientPropagatesEffectiveLanguage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(AcceptLanguageHeader); got != AmericanEnglish {
			t.Errorf("Accept-Language = %q, want %q", got, AmericanEnglish)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := NewHTTPClientWithRawClient(server.Client())
	ctx := WithLanguage(context.Background(), AmericanEnglish)
	status, _, err := client.GetNoUnmarshal(ctx, server.URL, nil, nil)
	if err != nil {
		t.Fatalf("GetNoUnmarshal() error = %v", err)
	}
	if status != http.StatusNoContent {
		t.Fatalf("GetNoUnmarshal() status = %d, want %d", status, http.StatusNoContent)
	}
}

func TestHTTPClientPreservesExplicitAcceptLanguage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(AcceptLanguageHeader); got != SimplifiedChinese {
			t.Errorf("Accept-Language = %q, want %q", got, SimplifiedChinese)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := NewHTTPClientWithRawClient(server.Client())
	ctx := WithLanguage(context.Background(), AmericanEnglish)
	status, _, err := client.GetNoUnmarshal(ctx, server.URL, nil, map[string]string{
		AcceptLanguageHeader: SimplifiedChinese,
	})
	if err != nil {
		t.Fatalf("GetNoUnmarshal() error = %v", err)
	}
	if status != http.StatusNoContent {
		t.Fatalf("GetNoUnmarshal() status = %d, want %d", status, http.StatusNoContent)
	}
}
