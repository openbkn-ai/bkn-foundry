// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package bknagent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

func TestTaskVersionRequiresExactPublishedToolFreeReceipt(t *testing.T) {
	for _, scenario := range []struct {
		body   string
		status int
		valid  bool
	}{
		{`{"agent_id":"document","agent_version":4,"prompt_version":2,"tool_free":true}`, 200, true},
		{`{"agent_id":"foreign","agent_version":4,"prompt_version":2,"tool_free":true}`, 200, false},
		{`{"agent_id":"document","agent_version":4,"prompt_version":null,"tool_free":true}`, 200, false},
		{`{"agent_id":"document","agent_version":4,"prompt_version":2,"tool_free":false}`, 200, false},
		{`{"agent_id":"document","agent_version":0,"prompt_version":2,"tool_free":true}`, 200, false},
		{`{}`, 403, false},
	} {
		t.Run(scenario.body, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || r.URL.Path != "/agents/document/task-version" || r.Header.Get("x-account-id") != "owner" || r.Header.Get("x-account-type") != "user" || r.Header.Get("Authorization") != "" {
					t.Error("wrong read-only identity boundary")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(scenario.status)
				_, _ = w.Write([]byte(scenario.body))
			}))
			defer server.Close()
			adapter := &bknAgentAccess{httpClient: rest.NewHTTPClient(), baseUrl: server.URL}
			version, err := adapter.CurrentTaskVersion(context.Background(), interfaces.BknAgentAccount{ID: "owner", Type: "user"}, "document")
			if calls != 1 || scenario.valid && (err != nil || version != 4) || !scenario.valid && err == nil {
				t.Fatal("invalid version receipt accepted", version, err)
			}
		})
	}
}
