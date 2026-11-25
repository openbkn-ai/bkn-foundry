// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_index_sync

import (
	"encoding/json"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	"github.com/stretchr/testify/require"
)

func TestPrepare(t *testing.T) {
	actor := interfaces.AccountInfo{ID: "alice", Type: "user"}
	envelope := func(committed bool, keys any) any {
		return map[string]any{"data_changes": map[string]any{"committed": committed, "affected_resources": []any{map[string]any{"resource_id": "brands", "keys": keys}}}}
	}
	t.Run("committed composite and generated keys keep numeric precision", func(t *testing.T) {
		result := interfaces.ObjectExecutionResult{Status: "success", Result: envelope(true, []any{map[string]any{"tenant": "cn", "id": json.Number("9007199254740993")}, map[string]any{"tenant": "cn", "id": json.Number("9007199254740994")}})}
		Prepare(&result, actor)
		require.Equal(t, "pending", result.IndexSync.Status)
		require.Equal(t, actor, result.IndexSync.Executor)
		require.Equal(t, json.Number("9007199254740993"), result.IndexSync.Tasks[0].Keys[0]["id"])
		require.Len(t, result.IndexSync.Tasks[0].Keys, 2)
	})
	t.Run("large scopes are chunked without dropping or synthesizing identities", func(t *testing.T) {
		keys := make([]map[string]any, 201)
		for i := range keys {
			keys[i] = map[string]any{"id": i}
		}
		result := interfaces.ObjectExecutionResult{Status: "success", Result: envelope(true, keys)}
		Prepare(&result, actor)
		require.Equal(t, "pending", result.IndexSync.Status)
		require.Len(t, result.IndexSync.Tasks, 3)
		require.Len(t, result.IndexSync.Tasks[0].Keys, 100)
		require.Len(t, result.IndexSync.Tasks[2].Keys, 1)
	})
	for _, tc := range []struct {
		name, status string
		output       any
		want         string
	}{
		{"ordinary output", "success", map[string]any{"id": 1}, ""},
		{"business failure", "failed", envelope(true, []any{map[string]any{"id": 1}}), ""},
		{"accepted but not committed", "success", envelope(false, []any{map[string]any{"id": 1}}), "invalid"},
		{"empty keys", "success", envelope(true, []any{}), "invalid"},
		{"null key", "success", envelope(true, []any{map[string]any{"id": nil}}), "invalid"},
		{"object key", "success", envelope(true, []any{map[string]any{"id": map[string]any{"nested": 1}}}), "invalid"},
		{"malformed declaration", "success", map[string]any{"data_changes": "bad"}, "invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := interfaces.ObjectExecutionResult{Status: tc.status, Result: tc.output}
			Prepare(&result, actor)
			if tc.want == "" {
				require.Nil(t, result.IndexSync)
			} else {
				require.Equal(t, tc.want, result.IndexSync.Status)
			}
			require.Equal(t, tc.status, result.Status)
		})
	}
	t.Run("MCP structured content and existing synchronization state", func(t *testing.T) {
		result := interfaces.ObjectExecutionResult{Status: "success", Result: map[string]any{"structuredContent": envelope(true, []any{map[string]any{"id": "new-id"}})}}
		Prepare(&result, actor)
		require.Equal(t, "pending", result.IndexSync.Status)
		result.IndexSync.Status = "completed"
		Prepare(&result, actor)
		require.Equal(t, "completed", result.IndexSync.Status)
	})
}

func TestPrepareFunction(t *testing.T) {
	result := interfaces.ObjectExecutionResult{Status: "success", Result: map[string]any{"stdout": `{"data_changes":{"committed":true,"affected_resources":[{"resource_id":"brands","keys":[{"id":662}]}]}}`}}
	PrepareFunction(&result, interfaces.AccountInfo{ID: "alice", Type: "user"})
	require.Equal(t, "pending", result.IndexSync.Status)
	require.Contains(t, result.Result.(map[string]any), "stdout")
}
