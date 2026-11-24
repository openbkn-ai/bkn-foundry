// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package vega_backend

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	rmock "github.com/openbkn-ai/bkn-foundry/comm-go/rest/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestVegaBackendAccessCreateKeyBuild(t *testing.T) {
	ctrl := gomock.NewController(t)
	httpClient := rmock.NewMockHTTPClient(ctrl)
	access := &vegaBackendAccess{baseURL: "http://vega", httpClient: httpClient}
	ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "alice", Type: "user"})
	keys := []map[string]any{{"id": json.Number("9007199254740993")}}
	httpClient.EXPECT().PostNoUnmarshal(gomock.Any(), "http://vega/build-tasks", gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, _ string, headers map[string]string, request any) (int, []byte, error) {
		require.Equal(t, "alice", headers[interfaces.HTTP_HEADER_ACCOUNT_ID])
		require.Empty(t, headers[interfaces.HTTPHeaderBKNProxyVersion])
		body := request.(map[string]any)
		require.Equal(t, "keys", body["execute_type"])
		require.Equal(t, "brands", body["resource_id"])
		require.Equal(t, keys, body["keys"])
		return http.StatusCreated, []byte(`{"id":"task"}`), nil
	})
	id, err := access.CreateKeyBuild(ctx, "brands", keys)
	require.NoError(t, err)
	require.Equal(t, "task", id)
	_, err = access.CreateKeyBuild(context.Background(), "brands", keys)
	require.Error(t, err)
}

func TestVegaBackendAccessGetKeyBuild(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
		body string
		ok   bool
	}{
		{"search visible", 200, `{"status":"completed","execute_type":"keys"}`, true},
		{"foreign task kind", 200, `{"status":"completed","execute_type":"full"}`, false},
		{"permissions revoked", 403, `{"message":"private detail"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			client := rmock.NewMockHTTPClient(ctrl)
			access := &vegaBackendAccess{baseURL: "http://vega", httpClient: client}
			client.EXPECT().GetNoUnmarshal(gomock.Any(), "http://vega/build-tasks/task", nil, gomock.Any()).Return(tc.code, []byte(tc.body), nil)
			ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "alice", Type: "user"})
			status, err := access.GetKeyBuild(ctx, "task")
			if tc.ok {
				require.NoError(t, err)
				require.Equal(t, "completed", status)
			} else {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "private detail")
			}
		})
	}
}

func TestVegaBackendAccessRetryKeyBuild(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := rmock.NewMockHTTPClient(ctrl)
	access := &vegaBackendAccess{baseURL: "http://vega", httpClient: client}
	client.EXPECT().PostNoUnmarshal(gomock.Any(), "http://vega/build-tasks/task/start", gomock.Any(), map[string]any{"reset": false}).Return(http.StatusAccepted, []byte(nil), nil)
	ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "alice", Type: "user"})
	require.NoError(t, access.RetryKeyBuild(ctx, "task"))
}
