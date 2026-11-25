// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package rest

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
)

func TestHTTPClientPreservesJSONNumbers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":9007199254740993}`)
	}))
	defer server.Close()

	client := NewHTTPClientWithRawClient(server.Client())
	status, data, err := client.Get(context.Background(), server.URL, nil, nil)
	if err != nil || status != http.StatusOK {
		t.Fatalf("Get() status = %d, error = %v", status, err)
	}
	body, ok := data.(map[string]any)
	if !ok {
		t.Fatalf("Get() data = %T, want map[string]any", data)
	}
	if got, want := body["id"], json.Number("9007199254740993"); got != want {
		t.Fatalf("id = %v (%T), want %v (%T)", got, got, want, want)
	}
}

func TestHTTPClientPropagatesEffectiveLanguage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(i18n.AcceptLanguageHeader); got != string(i18n.AmericanEnglish) {
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
		if got := r.Header.Get(i18n.AcceptLanguageHeader); got != string(i18n.AmericanEnglish) {
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
