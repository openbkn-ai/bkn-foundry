// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package opensearch

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type spanBoundedTransport func(*http.Request) (*http.Response, error)

func (f spanBoundedTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestSearchBoundedAuthBudgetAndErrorPrivacy(t *testing.T) {
	for _, status := range []int{200, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			c := NewWithHTTPClient("http://test", AuthConfig{Enabled: true, Username: "u", Password: "p"}, &http.Client{Transport: spanBoundedTransport(func(r *http.Request) (*http.Response, error) {
				u, p, ok := r.BasicAuth()
				if !ok || u != "u" || p != "p" || r.Method != "POST" || r.URL.Path != "/spans/_search" {
					t.Fatal("request contract")
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(strings.Repeat("secret", MaxBoundedSearchBytes/6+1)))}, nil
			})})
			_, e := c.SearchBounded(context.Background(), "spans", []byte(`{}`))
			if status == 200 && !errors.Is(e, ErrSearchResponseBudget) {
				t.Fatal(e)
			}
			if status == 503 && (e == nil || strings.Contains(e.Error(), "secret")) {
				t.Fatal("response leaked", e)
			}
		})
	}
}
