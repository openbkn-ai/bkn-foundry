// Copyright 2026 openbkn.ai
// Copyright The kweaver.ai Authors.
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
