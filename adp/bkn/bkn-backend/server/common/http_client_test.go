// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package common_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

func TestHTTPClientUsesEffectiveLocale(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(i18n.AcceptLanguageHeader); got != string(i18n.AmericanEnglish) {
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

func TestHTTPClientPreservesJSONRequestShape(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		const want = `{"paging":{"limit":100}}`
		if string(got) != want {
			t.Errorf("%s request body = %s, want %s", r.Method, got, want)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := rest.NewHTTPClientWithRawClient(server.Client())
	body := interfaces.ResourceDataQueryParams{Paging: interfaces.ResourceDataPagingRequest{Limit: 100}}
	if _, _, err := client.PostNoUnmarshal(context.Background(), server.URL, nil, body); err != nil {
		t.Fatal(err)
	}
	if _, _, err := client.PutNoUnmarshal(context.Background(), server.URL, nil, body); err != nil {
		t.Fatal(err)
	}
}
