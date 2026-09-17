// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// A Function-backed logic property or action reads BKN as its caller, so the
// caller's credential travels to ontology-query beside the account headers.
func TestFunctionBackedCallsForwardCallerCredential(t *testing.T) {
	for _, tc := range []struct {
		name  string
		ctx   context.Context
		want  string
		wants bool
	}{
		{name: "caller token", ctx: common.SetRawTokenToCtx(context.Background(), "caller-token"), want: "Bearer caller-token", wants: true},
		{name: "no token", ctx: context.Background()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got map[string]string
			client := &ontologyQueryClient{
				logger:  &mockLogger{},
				baseURL: "http://ontology.example.com",
				httpClient: &mockHTTPClient{
					bytesFunc: func(_ context.Context, _, _ string, headers map[string]string, _ interface{}) (int, []byte, error) {
						got = headers
						return 200, []byte(`{"datas":[]}`), nil
					},
				},
			}
			check := func(call string) {
				t.Helper()
				value, ok := got["Authorization"]
				if ok != tc.wants || value != tc.want {
					t.Fatalf("%s Authorization = %q (present %v), want %q", call, value, ok, tc.want)
				}
			}
			if _, err := client.QueryLogicProperties(tc.ctx, &interfaces.QueryLogicPropertiesReq{KnID: "kn-1", OtID: "ot-1"}); err != nil {
				t.Fatal(err)
			}
			check("QueryLogicProperties")
			got = nil
			if _, err := client.ExecuteActions(tc.ctx, &interfaces.ExecuteActionsRequest{KnID: "kn-1", AtID: "at-1"}); err != nil {
				t.Fatal(err)
			}
			check("ExecuteActions")
		})
	}
}
