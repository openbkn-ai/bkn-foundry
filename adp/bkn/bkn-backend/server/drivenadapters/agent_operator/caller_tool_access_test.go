// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package agent_operator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	rmock "github.com/openbkn-ai/bkn-foundry/comm-go/rest/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

func callerContext() context.Context {
	return context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "u1", Type: "user"})
}

func TestGetCallerToolReadsACypherFunction(t *testing.T) {
	ctrl := gomock.NewController(t)
	httpClient := rmock.NewMockHTTPClient(ctrl)
	httpClient.EXPECT().GetNoUnmarshal(gomock.Any(), "http://execution/caller/tool-box/box-1/tool/tool-1", nil, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ url.Values, headers map[string]string) (int, []byte, error) {
			assert.Equal(t, "u1", headers[interfaces.HTTP_HEADER_ACCOUNT_ID])
			assert.Equal(t, "user", headers[interfaces.HTTP_HEADER_ACCOUNT_TYPE])
			return http.StatusOK, []byte(`{"tool_id":"tool-1","name":"overdue","status":"enabled","update_time":1731628800000000000,
				"metadata_type":"function","metadata":{"function_content":{"script_type":"cypher","kn_id":"kn1",
				"result_shape":"table","code":"MATCH (o) RETURN o","inputs":[{"name":"today","type":"string","required":true}]}}}`), nil
		})
	access := &agentOperatorAccess{agentOperatorURL: "http://execution", httpClient: httpClient}

	tool, err := access.GetCallerTool(callerContext(), "box-1", "tool-1")

	require.NoError(t, err)
	assert.Equal(t, &interfaces.CallerTool{
		ToolID: "tool-1", Name: "overdue", Status: "enabled", UpdateTime: 1731628800000000000,
		ScriptType: "cypher", KnID: "kn1", ResultShape: "table",
		Inputs: []interfaces.CallerToolInput{{Name: "today", Type: "string", Required: true}},
	}, tool)
}

func TestGetCallerToolKeepsTheRefusalStatus(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound, http.StatusBadRequest} {
		ctrl := gomock.NewController(t)
		httpClient := rmock.NewMockHTTPClient(ctrl)
		httpClient.EXPECT().GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(status, []byte(`{"secret":"policy"}`), nil)
		access := &agentOperatorAccess{agentOperatorURL: "http://execution", httpClient: httpClient}

		_, err := access.GetCallerTool(callerContext(), "box-1", "tool-1")

		var dependencyErr *interfaces.DependencyError
		require.ErrorAs(t, err, &dependencyErr)
		assert.Equal(t, status, dependencyErr.HTTPStatus)
		assert.NotContains(t, err.Error(), "secret")
	}
}

// An account the execution factory does not know, such as a check subject
// deleted since, is answered 404 like a missing tool. It is an account that may
// not use the tool, so it must read as 403, not as a tool that does not exist.
func TestCallerRoutesReadAnUnknownAccountAsForbidden(t *testing.T) {
	unknownAccount := []byte(`{"error_code":"AgentOperatorIntegration.NotFound.CommonUserNotFound","description":"用户不存在请联系管理员"}`)
	missingTool := []byte(`{"error_code":"AgentOperatorIntegration.NotFound.ToolBoxNotFound"}`)
	for _, c := range []struct {
		name string
		body []byte
		want int
	}{
		{"unknown account", unknownAccount, http.StatusForbidden},
		{"missing tool", missingTool, http.StatusNotFound},
		{"unreadable body", []byte(`not json`), http.StatusNotFound},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			httpClient := rmock.NewMockHTTPClient(ctrl)
			httpClient.EXPECT().GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(http.StatusNotFound, c.body, nil)
			httpClient.EXPECT().PostNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(http.StatusNotFound, c.body, nil)
			access := &agentOperatorAccess{agentOperatorURL: "http://execution", httpClient: httpClient}

			_, getErr := access.GetCallerTool(callerContext(), "box-1", "tool-1")
			_, runErr := access.ExecuteCallerTool(callerContext(), "box-1", "tool-1", nil, time.Second)

			for _, err := range []error{getErr, runErr} {
				var dependencyErr *interfaces.DependencyError
				require.ErrorAs(t, err, &dependencyErr)
				assert.Equal(t, c.want, dependencyErr.HTTPStatus)
			}
		})
	}
}

func TestExecuteCallerToolSendsTheArgumentsAsTheBody(t *testing.T) {
	ctrl := gomock.NewController(t)
	httpClient := rmock.NewMockHTTPClient(ctrl)
	httpClient.EXPECT().PostNoUnmarshal(gomock.Any(), "http://execution/caller/tool-box/box-1/proxy/tool-1", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, headers map[string]string, request any) (int, []byte, error) {
			assert.Equal(t, "u1", headers[interfaces.HTTP_HEADER_ACCOUNT_ID])
			// Numbers go as they were written.
			assert.JSONEq(t, `{"timeout":90,"body":{"today":"2026-11-15","id":12345678901234567890}}`, string(request.([]byte)))
			return http.StatusOK, []byte(`{"status_code":200,"headers":{},"body":{"columns":[{"name":"key"}],
				"entries":[{"key":"o1"}],"limited":false},"error":"","duration_ms":12}`), nil
		})
	access := &agentOperatorAccess{agentOperatorURL: "http://execution", httpClient: httpClient}

	reply, err := access.ExecuteCallerTool(callerContext(), "box-1", "tool-1",
		map[string]any{"today": "2026-11-15", "id": json.Number("12345678901234567890")}, 90*time.Second)

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, reply.StatusCode)
	assert.JSONEq(t, `{"columns":[{"name":"key"}],"entries":[{"key":"o1"}],"limited":false}`, string(reply.Body))
}

func TestExecuteCallerToolReturnsWhatTheToolSaid(t *testing.T) {
	ctrl := gomock.NewController(t)
	httpClient := rmock.NewMockHTTPClient(ctrl)
	// The tool ran and refused: the execution factory still answers 200.
	httpClient.EXPECT().PostNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(http.StatusOK, []byte(`{"status_code":400,"body":{"error_code":"x","description":"missing"},
			"error":"missing required parameters: today"}`), nil)
	access := &agentOperatorAccess{agentOperatorURL: "http://execution", httpClient: httpClient}

	reply, err := access.ExecuteCallerTool(callerContext(), "box-1", "tool-1", nil, time.Second)

	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, reply.StatusCode)
	assert.Equal(t, "missing required parameters: today", reply.Error)
}

func TestExecuteCallerToolKeepsTheRefusalStatus(t *testing.T) {
	ctrl := gomock.NewController(t)
	httpClient := rmock.NewMockHTTPClient(ctrl)
	httpClient.EXPECT().PostNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(http.StatusForbidden, []byte(`{"error_code":"forbidden"}`), nil)
	access := &agentOperatorAccess{agentOperatorURL: "http://execution", httpClient: httpClient}

	_, err := access.ExecuteCallerTool(callerContext(), "box-1", "tool-1", nil, time.Second)

	var dependencyErr *interfaces.DependencyError
	require.ErrorAs(t, err, &dependencyErr)
	assert.Equal(t, http.StatusForbidden, dependencyErr.HTTPStatus)
	assert.Equal(t, interfaces.DependencyForbidden, dependencyErr.Kind)
}
