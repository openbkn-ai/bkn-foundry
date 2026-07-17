package bkn_agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
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
		assert.Equal(t, []string{"confidence", "logic_views", "obsolete_logic_views", "warnings"}, format["required"])
		logicViews := format["properties"].(map[string]any)["logic_views"].(map[string]any)
		assert.NotContains(t, logicViews, "maxItems")
		logicView := logicViews["items"].(map[string]any)
		assert.Equal(t, []string{"create", "update"}, logicView["properties"].(map[string]any)["action"].(map[string]any)["enum"])
	})

	t.Run("unsupported scope", func(t *testing.T) {
		format, err := semanticUnderstandingResponseFormat("unknown")

		require.ErrorContains(t, err, "unsupported semantic understanding task scope")
		assert.Nil(t, format)
	})
}
