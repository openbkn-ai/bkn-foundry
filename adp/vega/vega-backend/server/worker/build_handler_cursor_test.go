// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"testing"

	"vega-backend/interfaces"
)

// 复现 bug：批读游标必须随每批推进。此前实现用 `for _, kv := range` 修改
// range 副本，游标永远停在第一批末尾，第二批起 gt 过滤条件不变，
// 无限重读同一区间（synced_count 远超 total_count、重复写压垮索引）。
func TestAdvanceCursorAdvancesAcrossBatches(t *testing.T) {
	keys := []string{"key_id"}

	// 第一批：空游标初始化为本批最后一行
	cursor := advanceCursor(nil, keys, map[string]any{"key_id": "1000"})
	if len(cursor) != 1 || cursor[0].Value != "1000" {
		t.Fatalf("first batch: expected cursor key_id=1000, got %+v", cursor)
	}

	// 第二批：游标必须推进到新批次最后一行（bug 时停留在 1000）
	cursor = advanceCursor(cursor, keys, map[string]any{"key_id": "2000"})
	if cursor[0].Value != "2000" {
		t.Fatalf("second batch: cursor stuck at %v, expected 2000", cursor[0].Value)
	}

	// 第三批：持续推进
	cursor = advanceCursor(cursor, keys, map[string]any{"key_id": "3000"})
	if cursor[0].Value != "3000" {
		t.Fatalf("third batch: cursor stuck at %v, expected 3000", cursor[0].Value)
	}
}

// 多键游标：每个键都取本批最后一行的对应值。
func TestAdvanceCursorMultipleKeys(t *testing.T) {
	keys := []string{"id", "name"}
	cursor := advanceCursor(nil, keys, map[string]any{"id": 1, "name": "a"})
	cursor = advanceCursor(cursor, keys, map[string]any{"id": 2, "name": "b"})

	got := map[string]any{}
	for _, kv := range cursor {
		got[kv.Key] = kv.Value
	}
	if got["id"] != 2 || got["name"] != "b" {
		t.Fatalf("expected id=2 name=b, got %+v", got)
	}
}

var _ = interfaces.KeyValue{} // 保持 import 稳定

// reconcileFulltextFeatures 把 fulltext 特性对账到指定字段，analyzer 进 config，幂等。
func TestReconcileFulltextFeatures(t *testing.T) {
	res := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{
		{Name: "team_name", Type: interfaces.DataType_String},
		{Name: "team_code", Type: interfaces.DataType_String},
	}}
	changed := reconcileFulltextFeatures(res, "team_name", "ik_max_word")
	if !changed {
		t.Fatal("expected changed=true")
	}
	tn := res.SchemaDefinition[0]
	if len(tn.Features) != 1 || tn.Features[0].FeatureType != interfaces.PropertyFeatureType_Fulltext {
		t.Fatalf("team_name must get fulltext feature, got %+v", tn.Features)
	}
	if tn.Features[0].Config["analyzer"] != "ik_max_word" {
		t.Fatalf("analyzer must be in config, got %v", tn.Features[0].Config)
	}
	if len(res.SchemaDefinition[1].Features) != 0 {
		t.Fatalf("unselected field must be untouched, got %+v", res.SchemaDefinition[1].Features)
	}
	// 幂等：同配置再对账无改动
	if reconcileFulltextFeatures(res, "team_name", "ik_max_word") {
		t.Fatal("re-reconcile same config must be idempotent (changed=false)")
	}
}

// 编辑去掉字段：集合外字段的残留 fulltext 特性必须被移除。
func TestReconcileFulltextFeatures_RemovesStale(t *testing.T) {
	res := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{
		{Name: "team_name", Type: interfaces.DataType_String, Features: []interfaces.PropertyFeature{
			{FeatureName: "fulltext", FeatureType: interfaces.PropertyFeatureType_Fulltext, Config: map[string]any{"analyzer": "standard"}},
		}},
		{Name: "federation_name", Type: interfaces.DataType_String},
	}}
	// 新配置只保留 federation_name
	changed := reconcileFulltextFeatures(res, "federation_name", "standard")
	if !changed {
		t.Fatal("expected changed=true")
	}
	if hasFulltextFeature(res.SchemaDefinition[0]) {
		t.Fatalf("team_name fulltext must be removed, got %+v", res.SchemaDefinition[0].Features)
	}
	if !hasFulltextFeature(res.SchemaDefinition[1]) {
		t.Fatalf("federation_name must gain fulltext")
	}
	// 清空配置：所有残留都移除
	if !reconcileFulltextFeatures(res, "", "") {
		t.Fatal("clearing all fulltext must report changed")
	}
	if hasFulltextFeature(res.SchemaDefinition[1]) {
		t.Fatal("empty fulltext_fields must strip all fulltext features")
	}
}

// analyzer 改变需校正 config（用户改分词器，重建生效）。
func TestReconcileFulltextFeatures_UpdatesAnalyzer(t *testing.T) {
	res := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{
		{Name: "x", Type: interfaces.DataType_String, Features: []interfaces.PropertyFeature{
			{FeatureName: "fulltext", FeatureType: interfaces.PropertyFeatureType_Fulltext, Config: map[string]any{"analyzer": "standard"}},
		}},
	}}
	if !reconcileFulltextFeatures(res, "x", "ik_max_word") {
		t.Fatal("analyzer change must report changed")
	}
	if res.SchemaDefinition[0].Features[0].Config["analyzer"] != "ik_max_word" {
		t.Fatalf("analyzer must update, got %v", res.SchemaDefinition[0].Features[0].Config)
	}
}

// analyzer 为空时不写 config（走 OpenSearch 默认分词器）。
func TestReconcileFulltextFeatures_NoAnalyzerNoConfig(t *testing.T) {
	res := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{
		{Name: "x", Type: interfaces.DataType_String},
	}}
	reconcileFulltextFeatures(res, "x", "")
	if res.SchemaDefinition[0].Features[0].Config != nil {
		t.Fatalf("empty analyzer must leave config nil, got %v", res.SchemaDefinition[0].Features[0].Config)
	}
}

func TestFieldNameSet(t *testing.T) {
	got := fieldNameSet(" a, b ,, c ")
	if len(got) != 3 || !got["a"] || !got["b"] || !got["c"] {
		t.Fatalf("expected {a,b,c}, got %+v", got)
	}
}
