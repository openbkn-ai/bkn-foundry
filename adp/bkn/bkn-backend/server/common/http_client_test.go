// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package common

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

func TestHTTPClientUsesEffectiveLocale(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(i18n.AcceptLanguageHeader); got != i18n.AmericanEnglish {
			t.Errorf("Accept-Language = %q, want %q", got, i18n.AmericanEnglish)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := rest.NewHTTPClientWithRawClient(server.Client())
	ctx := i18n.WithLanguage(context.Background(), i18n.AmericanEnglish)
	_, _, err := client.PostNoUnmarshal(ctx, server.URL, map[string]string{
		i18n.AcceptLanguageHeader: "zh-CN, en-US;q=0.8",
	}, map[string]string{"id": "resource-1"})
	if err != nil {
		t.Fatalf("PostNoUnmarshal() error = %v", err)
	}
}
