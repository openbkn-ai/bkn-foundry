// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package rest

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
)

// bigIntegerBody is the shape reported in openbkn-ai/bkn-studio#464: values that
// Post's interface{} hop rounds to 9.223372036854776e+18 and 1.8446744073709552e+19.
const bigIntegerBody = `{"datas":[{"id":9223372036854775808,"unsigned":18446744073709551615}]}`

func TestPostBytesReturnsBodyVerbatim(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(bigIntegerBody))
	}))
	defer srv.Close()

	client := NewHTTPClientWithOptions(HTTPClientOptions{TimeOut: 5})
	code, body, err := client.PostBytes(context.Background(), srv.URL, nil, map[string]string{})
	if err != nil {
		t.Fatalf("PostBytes() error = %v", err)
	}
	if code != http.StatusOK {
		t.Errorf("status = %d, want 200", code)
	}
	if string(body) != bigIntegerBody {
		t.Errorf("body = %s, want %s", body, bigIntegerBody)
	}
}

func TestGetBytesReturnsBodyVerbatim(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("need_total"); got != "true" {
			t.Errorf("need_total = %q, want true", got)
		}
		_, _ = w.Write([]byte(bigIntegerBody))
	}))
	defer srv.Close()

	client := NewHTTPClientWithOptions(HTTPClientOptions{TimeOut: 5})
	_, body, err := client.GetBytes(context.Background(), srv.URL, url.Values{"need_total": {"true"}}, nil)
	if err != nil {
		t.Fatalf("GetBytes() error = %v", err)
	}
	if string(body) != bigIntegerBody {
		t.Errorf("body = %s, want %s", body, bigIntegerBody)
	}
}

// Callers such as ontology_query.classifyQueryError re-map downstream 4xx by
// reading the HTTP code off the error, so PostBytes must keep raising the same
// *infraErr.HTTPError that Post does.
func TestPostBytesKeepsHTTPErrorSemantics(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"detail":"downstream said no"}`))
		}))

		_, body, err := NewHTTPClientWithOptions(HTTPClientOptions{TimeOut: 5}).
			PostBytes(context.Background(), srv.URL, nil, map[string]string{})
		srv.Close()

		var he *infraErr.HTTPError
		if !errors.As(err, &he) {
			t.Fatalf("status %d: error = %v, want *infraErr.HTTPError", status, err)
		}
		if he.HTTPCode != status {
			t.Errorf("status %d: HTTPCode = %d", status, he.HTTPCode)
		}
		if string(body) == "" {
			t.Errorf("status %d: body was dropped, callers log it", status)
		}
	}
}

// An empty body is not a decode failure: httpDo leaves the caller with a nil
// payload and no error, and PostBytes must not diverge from that.
func TestPostBytesAcceptsEmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	_, body, err := NewHTTPClientWithOptions(HTTPClientOptions{TimeOut: 5}).
		PostBytes(context.Background(), srv.URL, nil, map[string]string{})
	if err != nil {
		t.Fatalf("PostBytes() error = %v", err)
	}
	if len(body) != 0 {
		t.Errorf("body = %s, want empty", body)
	}
}
