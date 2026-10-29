// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package bkn_agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

func TestSemanticUnderstandingResponseFormat(t *testing.T) {
	t.Run("resource", func(t *testing.T) {
		format, err := semanticUnderstandingResponseFormat(interfaces.SemanticUnderstandingTaskScopeResource)

		require.NoError(t, err)
		assert.Equal(t, "object", format["type"])
		assert.Equal(t, []string{"confidence", "resource", "fields", "warnings"}, format["required"])
		resource := format["properties"].(map[string]any)["resource"].(map[string]any)
		assert.Equal(t, []string{"display_name", "description"}, resource["required"])
		// api_name 可选且不带 pattern：结构化输出不合 schema 会让整条结果失败，格式由写回时校验。
		assert.Equal(t, map[string]any{"type": "string", "description": apiNameDescription}, resource["properties"].(map[string]any)["api_name"])
		field := format["properties"].(map[string]any)["fields"].(map[string]any)["items"].(map[string]any)
		assert.Equal(t, map[string]any{"type": "string", "description": apiNameDescription}, field["properties"].(map[string]any)["api_name"])
		assert.Equal(t, []string{"name", "display_name", "description"}, field["required"])
	})

	t.Run("catalog", func(t *testing.T) {
		format, err := semanticUnderstandingResponseFormat(interfaces.SemanticUnderstandingTaskScopeCatalog)

		require.NoError(t, err)
		assert.Equal(t, "object", format["type"])
		assert.Equal(t, []string{"confidence", "logical_views", "obsolete_logical_views", "warnings"}, format["required"])
		logicalViews := format["properties"].(map[string]any)["logical_views"].(map[string]any)
		assert.NotContains(t, logicalViews, "maxItems")
		logicalView := logicalViews["items"].(map[string]any)
		assert.Equal(t, []string{"create", "update"}, logicalView["properties"].(map[string]any)["action"].(map[string]any)["enum"])
	})

	t.Run("unsupported scope", func(t *testing.T) {
		format, err := semanticUnderstandingResponseFormat("unknown")

		require.ErrorContains(t, err, "unsupported semantic understanding task scope")
		assert.Nil(t, format)
	})
}
