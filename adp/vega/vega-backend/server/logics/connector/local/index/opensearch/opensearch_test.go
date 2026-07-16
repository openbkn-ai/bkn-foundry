// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package opensearch

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

func TestBuildFieldMappingsStringFulltextAddsTextSubfield(t *testing.T) {
	t.Run("string fulltext creates text subfield", func(t *testing.T) {
		c := &OpenSearchConnector{}
		schema := []*interfaces.Property{
			{
				Name: "team_name",
				Type: interfaces.DataType_String,
				Features: []interfaces.PropertyFeature{
					{
						FeatureName: "fulltext",
						FeatureType: interfaces.PropertyFeatureType_Fulltext,
						Config:      map[string]any{"analyzer": "ik_max_word"},
					},
				},
			},
		}

		props, _, err := c.buildFieldMappings(schema)

		require.NoError(t, err)
		field, _ := props["team_name"].(map[string]any)
		assert.Equal(t, "keyword", field["type"])
		fields, ok := field["fields"].(map[string]any)
		require.True(t, ok)
		sub, ok := fields["fulltext"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "text", sub["type"])
		assert.Equal(t, "ik_max_word", sub["analyzer"])
	})
}

func TestBuildFieldMappingsStringFulltextNoConfig(t *testing.T) {
	t.Run("string fulltext without config uses default analyzer", func(t *testing.T) {
		c := &OpenSearchConnector{}
		schema := []*interfaces.Property{
			{
				Name: "title",
				Type: interfaces.DataType_String,
				Features: []interfaces.PropertyFeature{
					{FeatureName: "fulltext", FeatureType: interfaces.PropertyFeatureType_Fulltext},
				},
			},
		}

		props, _, err := c.buildFieldMappings(schema)

		require.NoError(t, err)
		field := props["title"].(map[string]any)
		sub := field["fields"].(map[string]any)["fulltext"].(map[string]any)
		assert.Equal(t, "text", sub["type"])
		assert.NotContains(t, sub, "analyzer")
	})
}

func TestBuildFieldMappingsStringKeywordAndFulltext(t *testing.T) {
	t.Run("string keyword and fulltext keeps keyword config and text subfield", func(t *testing.T) {
		c := &OpenSearchConnector{}
		schema := []*interfaces.Property{
			{
				Name: "name",
				Type: interfaces.DataType_String,
				Features: []interfaces.PropertyFeature{
					{FeatureName: "kw", FeatureType: interfaces.PropertyFeatureType_Keyword, Config: map[string]any{"ignore_above": 256}},
					{FeatureName: "fulltext", FeatureType: interfaces.PropertyFeatureType_Fulltext, Config: map[string]any{"analyzer": "standard"}},
				},
			},
		}

		props, _, err := c.buildFieldMappings(schema)

		require.NoError(t, err)
		field := props["name"].(map[string]any)
		assert.Equal(t, "keyword", field["type"])
		assert.Equal(t, 256, field["ignore_above"])
		sub := field["fields"].(map[string]any)["fulltext"].(map[string]any)
		assert.Equal(t, "text", sub["type"])
		assert.Equal(t, "standard", sub["analyzer"])
	})
}

func TestBuildFieldMappingsTextFulltextSetsAnalyzer(t *testing.T) {
	t.Run("text fulltext sets analyzer on main field", func(t *testing.T) {
		c := &OpenSearchConnector{}
		schema := []*interfaces.Property{
			{
				Name: "body",
				Type: interfaces.DataType_Text,
				Features: []interfaces.PropertyFeature{
					{FeatureName: "fulltext", FeatureType: interfaces.PropertyFeatureType_Fulltext, Config: map[string]any{"analyzer": "hanlp_index"}},
				},
			},
		}

		props, _, err := c.buildFieldMappings(schema)

		require.NoError(t, err)
		field := props["body"].(map[string]any)
		assert.Equal(t, "text", field["type"])
		assert.Equal(t, "hanlp_index", field["analyzer"])
	})
}
