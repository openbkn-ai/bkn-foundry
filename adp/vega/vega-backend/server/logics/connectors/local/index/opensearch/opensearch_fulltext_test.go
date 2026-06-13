// Copyright 2026 openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package opensearch

import (
	"testing"

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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	field, _ := props["team_name"].(map[string]any)
	if field["type"] != "keyword" {
		t.Fatalf("string main field must stay keyword, got %v", field["type"])
	}
	fields, ok := field["fields"].(map[string]any)
	if !ok {
		t.Fatalf("expected text subfield, got none: %+v", field)
	}
	sub, ok := fields["fulltext"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'fulltext' subfield, got %+v", fields)
	}
	if sub["type"] != "text" {
		t.Fatalf("fulltext subfield must be text, got %v", sub["type"])
	}
	if sub["analyzer"] != "ik_max_word" {
		t.Fatalf("analyzer must propagate from config, got %v", sub["analyzer"])
	}
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	field := props["title"].(map[string]any)
	sub := field["fields"].(map[string]any)["fulltext"].(map[string]any)
	if sub["type"] != "text" {
		t.Fatalf("expected text subfield, got %v", sub["type"])
	}
	if _, has := sub["analyzer"]; has {
		t.Fatalf("no config should mean no analyzer key, got %v", sub["analyzer"])
	}
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	field := props["name"].(map[string]any)
	if field["type"] != "keyword" {
		t.Fatalf("main must be keyword, got %v", field["type"])
	}
	if field["ignore_above"] != 256 {
		t.Fatalf("keyword config must apply to main field, got %v", field["ignore_above"])
	}
	sub := field["fields"].(map[string]any)["fulltext"].(map[string]any)
	if sub["type"] != "text" || sub["analyzer"] != "standard" {
		t.Fatalf("fulltext subfield wrong: %+v", sub)
	}
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	field := props["body"].(map[string]any)
	if field["type"] != "text" {
		t.Fatalf("text main field must stay text, got %v", field["type"])
	}
	if field["analyzer"] != "hanlp_index" {
		t.Fatalf("analyzer must be set on text main field, got %v", field["analyzer"])
	}
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
	if got := fulltextFieldName(prop); got != "team_name.fulltext" {
		t.Fatalf("expected team_name.fulltext, got %s", got)
	}
}

// text 字段主字段即全文，用裸字段名。
func TestFulltextFieldName_TextUsesBareName(t *testing.T) {
	prop := &interfaces.Property{Name: "body", Type: interfaces.DataType_Text}
	if got := fulltextFieldName(prop); got != "body" {
		t.Fatalf("expected body, got %s", got)
	}
}

// string 字段无 fulltext 特性：用裸名（match 落到 keyword 主字段，行为不变）。
func TestFulltextFieldName_StringNoFulltextBareName(t *testing.T) {
	prop := &interfaces.Property{Name: "code", Type: interfaces.DataType_String}
	if got := fulltextFieldName(prop); got != "code" {
		t.Fatalf("expected code, got %s", got)
	}
}
