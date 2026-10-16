// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

func schemaSubject() context.Context {
	return common.SetAccountAuthContextToCtx(context.Background(), &interfaces.AccountAuthContext{
		AccountID: "user-a", AccountType: interfaces.AccessorTypeUser,
	})
}

func TestSchemaReadUsesConfiguredInternalBackendAndVerifiedIdentity(t *testing.T) {
	const raw = `{"display_key":"name","future":9007199254740993,"logic_properties":[{"analysis_dimensions":[{"name":"region"}]}]}`
	calls := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.EscapedPath() != "/api/bkn-backend/in/v1/knowledge-networks/kn-a%20b/object-types" {
			t.Errorf("wrong read path: %s %s", r.Method, r.URL.EscapedPath())
		}
		if r.URL.Query().Get("branch") != "dev" || r.URL.Query().Get("limit") != "-1" {
			t.Errorf("query lost: %s", r.URL.RawQuery)
		}
		if r.Header.Get("x-account-id") != "user-a" || r.Header.Get("user_id") != "user-a" || r.Header.Get("x-account-type") != "user" || r.Header.Get("Authorization") != "" {
			t.Error("identity or credentials incorrectly forwarded")
		}
		w.WriteHeader(403)
		io.WriteString(w, raw)
	}))
	defer backend.Close()
	client := newSchemaReadAccess(backend.URL + "/api/bkn-backend")
	status, body, err := client.ReadSchema(schemaSubject(), "kn-a b", "object-types", url.Values{"branch": {"dev"}, "limit": {"-1"}})
	if err != nil || status != 403 || string(body) != raw || calls != 1 {
		t.Fatalf("raw response lost: %d %s %v", status, body, err)
	}
}

func TestSchemaReadRejectsMissingSubjectAndUnknownCollections(t *testing.T) {
	client := newSchemaReadAccess("http://must-not-be-called.invalid")
	for _, tc := range []struct {
		ctx            context.Context
		kn, collection string
	}{
		{context.Background(), "kn", ""}, {schemaSubject(), "kn", "action-types"}, {schemaSubject(), "..", ""},
	} {
		status, _, err := client.ReadSchema(tc.ctx, tc.kn, tc.collection, nil)
		if err == nil || status != 0 {
			t.Fatalf("unsafe read was sent: %d %v", status, err)
		}
	}
}

func TestSchemaReadDoesNotFollowBackendRedirect(t *testing.T) {
	followed := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { followed = true; t.Error("identity followed a redirect") }))
	defer target.Close()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer backend.Close()
	status, _, err := newSchemaReadAccess(backend.URL).ReadSchema(schemaSubject(), "kn", "", nil)
	if err != nil || status != 302 || followed {
		t.Fatalf("redirect policy failed: %d %v", status, err)
	}
}

func TestSchemaReadHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(schemaSubject())
	cancel()
	_, _, err := newSchemaReadAccess("http://must-not-be-called.invalid").ReadSchema(ctx, "kn", "", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

type schemaRoundTripFunc func(*http.Request) (*http.Response, error)

func (f schemaRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSchemaReadBoundsResponseSize(t *testing.T) {
	client := newSchemaReadAccess("http://backend")
	client.client.Transport = schemaRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", (32<<20)+1)))}, nil
	})
	status, _, err := client.ReadSchema(schemaSubject(), "kn", "", nil)
	if status != 0 || err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("oversized schema accepted: %d %v", status, err)
	}
}

func TestSchemaReadForwardsV1MetricCataloguePages(t *testing.T) {
	for _, offset := range []string{"0", "500"} {
		backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/bkn-backend/in/v1/knowledge-networks/kn/object-metrics" || r.URL.Query().Get("offset") != offset || r.URL.Query().Get("status") != "published" || r.URL.Query().Get("limit") != "500" {
				t.Error("V1 catalogue read changed")
			}
			io.WriteString(w, `{"entries":[],"total_count":0}`)
		}))
		status, _, err := newSchemaReadAccess(backend.URL+"/api/bkn-backend").ReadSchema(schemaSubject(), "kn", "object-metrics", url.Values{"offset": {offset}, "status": {"published"}, "limit": {"500"}})
		backend.Close()
		if status != 200 || err != nil {
			t.Fatalf("V1 read failed: %d %v", status, err)
		}
	}
}

func TestSchemaReadRejectsPathSeparatorsBeforeTransport(t *testing.T) {
	for _, id := range []string{"", ".", "..", "kn/action-types", "kn/object-types/ot", "kn/sample-data", "kn/proxy-account", "kn/metrics/metric/execution-context", "/kn", "kn/", "kn\r", "kn\n"} {
		for _, collection := range []string{"", "object-types", "relation-types", "object-metrics"} {
			t.Run(id+"/"+collection, func(t *testing.T) {
				client := newSchemaReadAccess("http://backend")
				client.client.Transport = schemaRoundTripFunc(func(*http.Request) (*http.Response, error) {
					t.Fatal("invalid network id reached backend transport")
					return nil, errors.New("unexpected transport")
				})
				status, body, err := client.ReadSchema(schemaSubject(), id, collection, nil)
				if err == nil || status != 0 || body != nil {
					t.Fatalf("invalid id accepted: %d %s %v", status, body, err)
				}
			})
		}
	}
}
