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
		assert.Contains(t, logicalView["properties"], "logical_view_definition")
		assert.Contains(t, logicalView["required"], "logical_view_definition")
		assert.NotContains(t, logicalView["properties"], "logic_definition")
		assert.NotContains(t, logicalView["required"], "logic_definition")
	})

	t.Run("unsupported scope", func(t *testing.T) {
		format, err := semanticUnderstandingResponseFormat("unknown")

		require.ErrorContains(t, err, "unsupported semantic understanding task scope")
		assert.Nil(t, format)
	})
}
