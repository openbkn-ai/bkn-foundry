// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

func TestVegaRawQueryPreservesLargeIntegersAsJSONNumber(t *testing.T) {
	client := &vegaAccess{
		logger:  &mockLogger{},
		baseURL: "http://vega.example.com",
		httpClient: &mockHTTPClient{
			postNoUnmarshalFunc: func(_ context.Context, _ string, _ map[string]string, _ interface{}) (int, []byte, error) {
				return 200, []byte(`{"entries":[{"id_card":110101199001152345,"small":42}]}`), nil
			},
		},
	}

	resp, err := client.RawQuery(context.Background(), &interfaces.VegaRawQueryReq{})
	if err != nil {
		t.Fatalf("RawQuery() error = %v", err)
	}

	large, ok := resp.Entries[0]["id_card"].(json.Number)
	if !ok {
		t.Fatalf("id_card type = %T, want json.Number", resp.Entries[0]["id_card"])
	}
	if got := large.String(); got != "110101199001152345" {
		t.Errorf("id_card = %s, want 110101199001152345", got)
	}
	if _, ok := resp.Entries[0]["small"].(json.Number); !ok {
		t.Errorf("small type = %T, want json.Number", resp.Entries[0]["small"])
	}
}
