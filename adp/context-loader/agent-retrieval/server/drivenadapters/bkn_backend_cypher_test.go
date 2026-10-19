// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/mocks"
)

// #1545: bkn-backend refuses a Cypher query with 403 and a code of its own --
// a property the caller may not read in full, or a resource the data source
// refused. Both reach the caller with the same status and code, not as a
// gateway failure and not as a failed authentication of Context Loader itself.
func TestRunCypherQueryPassesForbiddenThrough(t *testing.T) {
	for _, code := range []string{"BknBackend.Cypher.PropertyForbidden", "BknBackend.Cypher.Forbidden"} {
		t.Run(code, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockLogger := mocks.NewMockLogger(ctrl)
			mockHTTPClient := mocks.NewMockHTTPClient(ctrl)
			mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
			mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()
			mockHTTPClient.EXPECT().
				PostNoUnmarshal(gomock.Any(), "http://bkn/in/v1/knowledge-networks/kn-1/cypher-queries", gomock.Any(), gomock.Any()).
				Return(http.StatusForbidden, []byte(`{"error_code":"`+code+`","description":"refused",`+
					`"solution":"ask","error_details":"ot_user.phone","metadata":{"permission_impacts":`+
					`[{"object_type_id":"ot_user","properties":["phone"]}]}}`), nil)

			client := &bknBackendAccess{logger: mockLogger, baseURL: "http://bkn", httpClient: mockHTTPClient}
			_, err := client.RunCypherQuery(context.Background(), &interfaces.CypherQueryReq{
				KnID: "kn-1", Query: "MATCH (u:user) RETURN u.phone",
			})

			var httpErr *infraErr.HTTPError
			if !errors.As(err, &httpErr) {
				t.Fatalf("RunCypherQuery() error = %T %v, want *HTTPError", err, err)
			}
			if httpErr.HTTPCode != http.StatusForbidden || httpErr.Code != code {
				t.Fatalf("RunCypherQuery() error = %d %s, want 403 %s", httpErr.HTTPCode, httpErr.Code, code)
			}
			if httpErr.ErrorDetails != "ot_user.phone" {
				t.Fatalf("details = %v, want the property the refusal names", httpErr.ErrorDetails)
			}
			if httpErr.Metadata == nil {
				t.Fatal("machine-readable permission metadata was lost")
			}
		})
	}
}

func TestRunCypherQueryCarriesPermissionImpactsOnSuccess(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockLogger := mocks.NewMockLogger(ctrl)
	mockHTTPClient := mocks.NewMockHTTPClient(ctrl)
	mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
	mockHTTPClient.EXPECT().
		PostNoUnmarshal(gomock.Any(), "http://bkn/in/v1/knowledge-networks/kn-1/cypher-queries", gomock.Any(), gomock.Any()).
		Return(http.StatusOK, []byte(`{"columns":[],"entries":[],"permission_impacts":`+
			`[{"object_type_id":"ot_order","row_filter_applied":true}]}`), nil)

	client := &bknBackendAccess{logger: mockLogger, baseURL: "http://bkn", httpClient: mockHTTPClient}
	resp, err := client.RunCypherQuery(context.Background(), &interfaces.CypherQueryReq{
		KnID: "kn-1", Query: "MATCH (o:Order) RETURN count(*)",
	})
	if err != nil {
		t.Fatalf("RunCypherQuery() error = %v", err)
	}
	if len(resp.PermissionImpacts) != 1 || resp.PermissionImpacts[0].ObjectTypeID != "ot_order" ||
		!resp.PermissionImpacts[0].RowFilterApplied {
		t.Fatalf("permission impacts = %+v", resp.PermissionImpacts)
	}
}

// Row filters are evaluated in bkn-backend. Context Loader must therefore
// retain the actual caller while creating its child tracing operation.
func TestRunCypherQueryForwardsCallerIdentity(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockLogger := mocks.NewMockLogger(ctrl)
	mockHTTPClient := mocks.NewMockHTTPClient(ctrl)
	mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()
	mockHTTPClient.EXPECT().
		PostNoUnmarshal(gomock.Any(), "http://bkn/in/v1/knowledge-networks/kn-1/cypher-queries", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, header map[string]string, _ any) (int, []byte, error) {
			if got := header[string(interfaces.HeaderXAccountID)]; got != "user-1" {
				t.Fatalf("x-account-id = %q, want user-1", got)
			}
			if got := header[string(interfaces.HeaderXAccountType)]; got != "user" {
				t.Fatalf("x-account-type = %q, want user", got)
			}
			return http.StatusOK, []byte(`{"columns":[],"entries":[]}`), nil
		})

	ctx := common.SetAccountAuthContextToCtx(context.Background(), &interfaces.AccountAuthContext{
		AccountID: "user-1", AccountType: interfaces.AccessorType("user"),
	})
	client := &bknBackendAccess{logger: mockLogger, baseURL: "http://bkn", httpClient: mockHTTPClient}
	if _, err := client.RunCypherQuery(ctx, &interfaces.CypherQueryReq{KnID: "kn-1", Query: "MATCH (u:user) RETURN u"}); err != nil {
		t.Fatalf("RunCypherQuery() error = %v", err)
	}
}

// A query that returns nodes asks for the properties they carry, and its
// answer describes its values; both cross Context Loader as bkn-backend wrote
// them.
func TestRunCypherQueryCarriesGraphValues(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockLogger := mocks.NewMockLogger(ctrl)
	mockHTTPClient := mocks.NewMockHTTPClient(ctrl)
	mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
	var sent *interfaces.CypherQueryReq
	mockHTTPClient.EXPECT().
		PostNoUnmarshal(gomock.Any(), "http://bkn/in/v1/knowledge-networks/kn-1/cypher-queries", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ map[string]string, body any) (int, []byte, error) {
			sent, _ = body.(*interfaces.CypherQueryReq)
			return http.StatusOK, []byte(`{"columns":[{"name":"team","type":"object"}],` +
				`"entries":[{"team":{"kind":"object","object_type_id":"teams","key":{"id":{"type":"integer","value":"9"}}}}],` +
				`"value_types":{"team":{"kind":"object","object_type_id":"teams"}},` +
				`"execution":{"match_rows":3,"match_complete":true,"conclusions_limited":false}}`), nil
		})

	client := &bknBackendAccess{logger: mockLogger, baseURL: "http://bkn", httpClient: mockHTTPClient}
	resp, err := client.RunCypherQuery(context.Background(), &interfaces.CypherQueryReq{
		KnID: "kn-1", Query: "MATCH (t:teams) RETURN t AS team",
		ObjectProperties: map[string][]string{"t": {"team_name"}},
	})
	if err != nil {
		t.Fatalf("RunCypherQuery() error = %v", err)
	}
	body, _ := json.Marshal(sent)
	if !strings.Contains(string(body), `"object_properties":{"t":["team_name"]}`) {
		t.Fatalf("request body = %s, want object_properties", body)
	}
	if string(resp.ValueTypes) != `{"team":{"kind":"object","object_type_id":"teams"}}` {
		t.Fatalf("value_types = %s", resp.ValueTypes)
	}
	if string(resp.Execution) != `{"match_rows":3,"match_complete":true,"conclusions_limited":false}` {
		t.Fatalf("execution = %s", resp.Execution)
	}
	out, _ := json.Marshal(resp)
	if !strings.Contains(string(out), `"value_types"`) || !strings.Contains(string(out), `"execution"`) {
		t.Fatalf("response = %s, want value_types and execution passed on", out)
	}
}
