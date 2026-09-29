// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/permissionguide"
)

type viewableSchemaStub struct{}

func (viewableSchemaStub) GetObjectTypeSchema(context.Context, string, string) (*interfaces.ObjectTypeSchemaResp, error) {
	return &interfaces.ObjectTypeSchemaResp{}, nil
}

func guidanceTestGuide() *permissionguide.Guide {
	return permissionguide.New(config.PermissionRequestConfig{
		PathTemplate: "/studio/knowledge-network/workspace/{kn_id}/object-types/{ot_id}/detail?requestPermission={scope_code}",
	}, viewableSchemaStub{}, nil)
}

func guidanceTestCtx() context.Context {
	ctx := common.SetAccountAuthContextToCtx(context.Background(), &interfaces.AccountAuthContext{
		AccountID: "u-1", AccountType: interfaces.AccessorTypeUser,
	})
	return common.SetPublicOriginToCtx(ctx, "https://bkn.example.com")
}

func TestQueryObjectInstanceRefusalCarriesPermissionGuidance(t *testing.T) {
	stub := &stubOntologyQuery{err: &infraErr.HTTPError{
		HTTPCode: http.StatusForbidden, Code: "Public.Forbidden",
		ErrorDetails: "Public.Forbidden: query_data was not granted for object_type:kn-1/ot-1",
	}}
	handler := handleQueryObjectInstance(stub, guidanceTestGuide())

	result, err := handler(guidanceTestCtx(), mcpReq(map[string]any{"kn_id": "kn-1", "ot_id": "ot-1"}))
	if err != nil || result == nil || !result.IsError {
		t.Fatalf("result = %+v, err = %v; want a tool error", result, err)
	}

	var envelope struct {
		Code     string                        `json:"code"`
		Details  string                        `json:"details"`
		Guidance interfaces.PermissionGuidance `json:"permission_guidance"`
	}
	if err := json.Unmarshal([]byte(resultText(result)), &envelope); err != nil {
		t.Fatalf("error text is not JSON: %v", err)
	}
	if envelope.Code != "Public.Forbidden" || !strings.Contains(envelope.Details, "query_data") {
		t.Fatalf("original error fields lost: %+v", envelope)
	}
	shortfalls := envelope.Guidance.Shortfalls
	if envelope.Guidance.Resource.ID != "kn-1/ot-1" || len(shortfalls) != 1 ||
		shortfalls[0].Scope != interfaces.PermissionScopeGrant ||
		shortfalls[0].RequestPermissionURL != "https://bkn.example.com/studio/knowledge-network/workspace/kn-1/object-types/ot-1/detail?requestPermission=1" {
		t.Fatalf("guidance = %+v", envelope.Guidance)
	}
}

func TestQueryObjectInstanceOtherErrorsStayUnchanged(t *testing.T) {
	downstream := &infraErr.HTTPError{HTTPCode: http.StatusBadRequest, Code: "Public.BadRequest", ErrorDetails: "bad"}
	handler := handleQueryObjectInstance(&stubOntologyQuery{err: downstream}, guidanceTestGuide())

	result, _ := handler(guidanceTestCtx(), mcpReq(map[string]any{"kn_id": "kn-1", "ot_id": "ot-1"}))

	if got := resultText(result); got != downstream.Error() {
		t.Fatalf("error text = %s, want %s", got, downstream.Error())
	}
}

func TestQueryObjectInstanceMaskedResultCarriesPermissionGuidance(t *testing.T) {
	stub := &stubOntologyQuery{resp: &interfaces.QueryObjectInstancesResp{
		Data: []any{map[string]any{"name": "张三", "phone": "138****0000"}},
		EffectivePermissions: map[string]interfaces.PropertyAccessLevel{
			"name": interfaces.PropertyAccessFull, "phone": interfaces.PropertyAccessMasked,
		},
		RowFilterApplied: true,
	}}
	handler := handleQueryObjectInstance(stub, guidanceTestGuide())

	result, err := handler(guidanceTestCtx(), mcpReq(map[string]any{
		"kn_id": "kn-1", "ot_id": "ot-1", "response_format": "json",
	}))
	if err != nil || result == nil || result.IsError {
		t.Fatalf("result = %+v, err = %v", result, err)
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal([]byte(resultText(result)), &body); err != nil {
		t.Fatalf("result text is not JSON: %v", err)
	}
	if _, echoed := body["row_filter_applied"]; echoed {
		t.Fatalf("row_filter_applied must be restated by the guidance, not echoed")
	}
	var guidance interfaces.PermissionGuidance
	if err := json.Unmarshal(body["permission_guidance"], &guidance); err != nil {
		t.Fatalf("permission_guidance missing: %v", err)
	}
	if len(guidance.Shortfalls) != 2 ||
		guidance.Shortfalls[0].Scope != interfaces.PermissionScopePropertyGrants ||
		guidance.Shortfalls[0].Properties[0] != "phone" ||
		guidance.Shortfalls[1].Scope != interfaces.PermissionScopeRowFilter {
		t.Fatalf("guidance = %+v", guidance)
	}
}

func TestGetLogicPropertiesRefusalCarriesPermissionGuidance(t *testing.T) {
	stub := &stubLogicPropertyResolverService{err: &infraErr.HTTPError{HTTPCode: http.StatusForbidden, Code: "Public.Forbidden"}}
	handler := handleGetLogicPropertiesValues(stub, guidanceTestGuide())

	result, _ := handler(guidanceTestCtx(), mcpReq(map[string]any{
		"kn_id": "kn-1", "ot_id": "ot-1", "properties": []any{"score"},
		"_instance_identities": []any{map[string]any{"id": "1"}},
	}))

	if result == nil || !result.IsError || !strings.Contains(resultText(result), `"permission_guidance"`) {
		t.Fatalf("result = %+v, want a refusal with guidance", result)
	}
}
