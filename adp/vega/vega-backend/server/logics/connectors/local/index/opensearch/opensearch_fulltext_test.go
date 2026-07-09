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

// string 字段带 fulltext 特性时，主字段仍为 keyword（精确匹配/排序不变），
// 同时挂一个 text 子字段做全文检索；analyzer 从 feature.config 注入。
// 复现 bug：此前 buildFieldMappings 对 fulltext 特性 `continue`，子字段从未生成。
func TestBuildFieldMappings_StringFulltextAddsTextSubfield(t *testing.T) {
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
}

// string + fulltext 无 config：仍建 text 子字段，用默认分词器（不设 analyzer）。
func TestBuildFieldMappings_StringFulltextNoConfig(t *testing.T) {
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
}

// string 同时带 keyword 与 fulltext：主字段 keyword(含 keyword config) + text 子字段。
func TestBuildFieldMappings_StringKeywordAndFulltext(t *testing.T) {
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
}

// text 字段带 fulltext：主字段已是 text(全文)，把 analyzer 设到主字段。
func TestBuildFieldMappings_TextFulltextSetsAnalyzer(t *testing.T) {
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
}

// match 查询命中 string 全文字段时必须用 `.fulltext` 子字段，否则落到 keyword 主字段做精确匹配。
func TestFulltextFieldName_StringUsesSubfield(t *testing.T) {
	prop := &interfaces.Property{
		Name: "team_name",
		Type: interfaces.DataType_String,
		Features: []interfaces.PropertyFeature{
			{FeatureName: "fulltext", FeatureType: interfaces.PropertyFeatureType_Fulltext},
		},
	}
	assert.Equal(t, "team_name.fulltext", fulltextFieldName(prop))
}

// text 字段主字段即全文，用裸字段名。
func TestFulltextFieldName_TextUsesBareName(t *testing.T) {
	prop := &interfaces.Property{Name: "body", Type: interfaces.DataType_Text}
	assert.Equal(t, "body", fulltextFieldName(prop))
}

// string 字段无 fulltext 特性：用裸名（match 落到 keyword 主字段，行为不变）。
func TestFulltextFieldName_StringNoFulltextBareName(t *testing.T) {
	prop := &interfaces.Property{Name: "code", Type: interfaces.DataType_String}
	assert.Equal(t, "code", fulltextFieldName(prop))
}
