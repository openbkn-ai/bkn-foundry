// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/kncypher"
)

type refusingCypher struct{ err error }

func (r refusingCypher) RunCypher(context.Context, *kncypher.RunCypherReq) (*interfaces.CypherQueryResp, error) {
	return nil, r.err
}

// #1545: a bkn-backend refusal comes back to the agent as a tool error carrying
// the compiler's own code and details, so the agent can tell a property it may
// not read, or a resource grant it lacks, from a query it wrote wrong.
func TestRunCypherToolCarriesForbiddenCode(t *testing.T) {
	handler := handleRunCypher(refusingCypher{err: &infraErr.HTTPError{
		HTTPCode: http.StatusForbidden, Code: "BknBackend.Cypher.PropertyForbidden",
		Description: "refused", ErrorDetails: "ot_user.phone",
	}})

	result, err := handler(context.Background(), newCallToolRequest(map[string]any{
		"kn_id": "kn-1", "query": "MATCH (u:user) RETURN u.phone", "response_format": "json",
	}))
	if err != nil {
		t.Fatalf("handler error = %v, want a tool error result", err)
	}
	if !result.IsError || len(result.Content) != 1 {
		t.Fatalf("result = %+v, want one error content", result)
	}
	text, ok := mcp.AsTextContent(result.Content[0])
	if !ok || !strings.Contains(text.Text, `"code":"BknBackend.Cypher.PropertyForbidden"`) ||
		!strings.Contains(text.Text, "ot_user.phone") {
		t.Fatalf("content = %+v, want the refusal's code and details", result.Content[0])
	}
}
