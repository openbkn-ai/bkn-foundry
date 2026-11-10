// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/knversion"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
)

// versionServer records the query of each request and answers with the headers
// bkn-backend sets on a versioned read.
func versionServer(t *testing.T, queries *[]string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*queries = append(*queries, r.URL.RawQuery)
		w.Header().Set(knversion.HeaderVersion, "draft")
		w.Header().Set(knversion.HeaderDraftRevision, "12")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestKnVersionClientWithoutSelectorChangesNothing(t *testing.T) {
	var queries []string
	srv := versionServer(t, &queries)
	c := newKnVersionClient(rest.NewHTTPClient())
	if _, _, err := c.GetNoUnmarshal(context.Background(), srv.URL+"/x?branch=main", nil, nil); err != nil {
		t.Fatal(err)
	}
	if queries[0] != "branch=main" {
		t.Fatalf("query = %q", queries[0])
	}
}

func TestKnVersionClientSendsTheVersionAndPinsFromTheFirstResponse(t *testing.T) {
	var queries []string
	srv := versionServer(t, &queries)
	c := newKnVersionClient(rest.NewHTTPClient())
	sel, _ := knversion.Resolve("draft", "")
	ctx := knversion.WithSelector(context.Background(), sel)

	// First call: what the caller asked for, and the redundant branch is dropped.
	if _, _, err := c.GetNoUnmarshal(ctx, srv.URL+"/x?branch=draft", nil, nil); err != nil {
		t.Fatal(err)
	}
	// Later calls, whatever the verb: the version and revision the first one saw.
	if _, _, err := c.PostNoUnmarshal(ctx, srv.URL+"/y", nil, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.GetBytes(ctx, srv.URL+"/z", nil, nil); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"kn_version=draft",
		"draft_revision=12&kn_version=draft",
		"draft_revision=12&kn_version=draft",
	}
	for i := range want {
		if queries[i] != want[i] {
			t.Errorf("call %d query = %q, want %q", i, queries[i], want[i])
		}
	}
}

func TestKnVersionClientLeavesProxyResolutionAlone(t *testing.T) {
	var queries []string
	srv := versionServer(t, &queries)
	c := newKnVersionClient(rest.NewHTTPClient())
	sel, _ := knversion.Resolve("3", "")
	ctx := knversion.WithSelector(context.Background(), sel)
	if _, _, err := c.PostNoUnmarshal(ctx, srv.URL+"/kn/proxy-account/resolve", nil, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if queries[0] != "" {
		t.Fatalf("query = %q", queries[0])
	}
}
