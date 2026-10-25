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

type searchBudgetBody struct {
	io.Reader
	read   int
	closed bool
}

func (b *searchBudgetBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.read += n
	return n, err
}
func (b *searchBudgetBody) Close() error { b.closed = true; return nil }
func TestSearchBoundedConfigurableLimit(t *testing.T) {
	for _, size := range []int{8, 9, 20} {
		body := &searchBudgetBody{Reader: strings.NewReader(strings.Repeat("a", size))}
		c := NewWithHTTPClient("http://test", AuthConfig{}, &http.Client{Transport: spanBoundedTransport(func(*http.Request) (*http.Response, error) { return &http.Response{StatusCode: 200, Body: body}, nil })})
		got, err := c.SearchBounded(context.Background(), "traces", []byte(`{}`), 8)
		if size == 8 {
			if err != nil || len(got) != 8 {
				t.Fatalf("exact boundary: %q, %v", got, err)
			}
		} else if !errors.Is(err, ErrSearchResponseBudget) || got != nil {
			t.Fatalf("oversize: %q, %v", got, err)
		}
		if body.read > 9 || !body.closed {
			t.Fatalf("body read %d, closed %v", body.read, body.closed)
		}
	}
}
func TestSearchBoundedRejectsInvalidLimitsBeforeIO(t *testing.T) {
	calls := 0
	c := NewWithHTTPClient("http://test", AuthConfig{}, &http.Client{Transport: spanBoundedTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})})
	for _, limit := range []int{0, -1, (16 << 20) + 1} {
		if _, err := c.SearchBounded(context.Background(), "traces", nil, limit); err == nil || errors.Is(err, ErrSearchResponseBudget) {
			t.Fatalf("invalid limit %d: %v", limit, err)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid limit made %d requests", calls)
	}
	if _, err := c.SearchBounded(context.Background(), "traces", nil, 16<<20); err != nil {
		t.Fatal(err)
	}
}

func TestSearchBoundedDoesNotChangeOrdinarySearch(t *testing.T) {
	body := strings.Repeat("a", MaxBoundedSearchBytes+1)
	c := NewWithHTTPClient("http://test", AuthConfig{}, &http.Client{Transport: spanBoundedTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})})
	if _, err := c.SearchBounded(context.Background(), "traces", nil); !errors.Is(err, ErrSearchResponseBudget) {
		t.Fatalf("default limit: %v", err)
	}
	if got, err := c.Search(context.Background(), "traces", nil); err != nil || len(got) != len(body) {
		t.Fatalf("ordinary Search changed: %d, %v", len(got), err)
	}
}
