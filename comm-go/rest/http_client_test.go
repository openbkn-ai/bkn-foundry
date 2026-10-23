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

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
)

func TestHTTPClientPropagatesEffectiveLanguage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(i18n.AcceptLanguageHeader); got != i18n.AmericanEnglish {
			t.Errorf("Accept-Language = %q, want %q", got, i18n.AmericanEnglish)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := NewHTTPClientWithRawClient(server.Client())
	ctx := i18n.WithLanguage(context.Background(), i18n.AmericanEnglish)
	status, _, err := client.GetNoUnmarshal(ctx, server.URL, nil, nil)
	if err != nil {
		t.Fatalf("GetNoUnmarshal() error = %v", err)
	}
	if status != http.StatusNoContent {
		t.Fatalf("GetNoUnmarshal() status = %d, want %d", status, http.StatusNoContent)
	}
}

func TestHTTPClientOverridesExplicitAcceptLanguageWithEffectiveLocale(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(i18n.AcceptLanguageHeader); got != i18n.AmericanEnglish {
			t.Errorf("Accept-Language = %q, want %q", got, i18n.AmericanEnglish)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := NewHTTPClientWithRawClient(server.Client())
	ctx := i18n.WithLanguage(context.Background(), i18n.AmericanEnglish)
	status, _, err := client.GetNoUnmarshal(ctx, server.URL, nil, map[string]string{
		i18n.AcceptLanguageHeader: "zh-CN, en-US;q=0.8",
	})
	if err != nil {
		t.Fatalf("GetNoUnmarshal() error = %v", err)
	}
	if status != http.StatusNoContent {
		t.Fatalf("GetNoUnmarshal() status = %d, want %d", status, http.StatusNoContent)
	}
}
