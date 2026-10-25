// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package opensearchtraceaccess

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ihttpaccess/tracequeryport"
)

type spanBudgetTransport func(*http.Request) (*http.Response, error)

func (f spanBudgetTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestSpanAdapterMapsActualBoundedResponseBudget(t *testing.T) {
	c := opensearch.NewWithHTTPClient("http://isolated", opensearch.AuthConfig{}, &http.Client{Transport: spanBudgetTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/spans/_search" {
			t.Fatal(r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", opensearch.MaxBoundedSearchBytes+1)))}, nil
	})})
	result, err := New(c, "spans").SearchTraceSpans(context.Background(), []byte(`{}`))
	if !errors.Is(err, tracequeryport.ErrSpanResponseBudget) || result != nil {
		t.Fatalf("result bytes=%d error=%v", len(result), err)
	}
}
