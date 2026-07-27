// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "testing"

// TestStripInstanceScores 覆盖 #236：query_object_instance 无相关度评分，
// 响应中的恒定 _score 必须被剥除；有无过滤条件的结果结构一致。
func TestStripInstanceScores(t *testing.T) {
	hasScore := func(item any) bool {
		m, ok := item.(map[string]any)
		if !ok {
			return false
		}
		_, ok = m["_score"]
		return ok
	}

	t.Run("nil resp 不 panic", func(t *testing.T) {
		var resp *QueryObjectInstancesResp
		resp.StripInstanceScores()
	})

	t.Run("空结果 不 panic", func(t *testing.T) {
		resp := &QueryObjectInstancesResp{Data: nil}
		resp.StripInstanceScores()
		if len(resp.Data) != 0 {
			t.Fatalf("expected empty data, got %d", len(resp.Data))
		}
	})

	t.Run("剥除 _score 保留其余字段", func(t *testing.T) {
		resp := &QueryObjectInstancesResp{
			Data: []any{
				map[string]any{"child_name": "产品A", "_score": 1.0},
				map[string]any{"child_name": "产品B", "_score": 1.0},
			},
		}
		resp.StripInstanceScores()
		for i, item := range resp.Data {
			if hasScore(item) {
				t.Errorf("item %d 仍含 _score", i)
			}
			if m := item.(map[string]any); m["child_name"] == nil {
				t.Errorf("item %d 丢失业务字段 child_name", i)
			}
		}
	})

	t.Run("无 _score 的结果原样保留", func(t *testing.T) {
		resp := &QueryObjectInstancesResp{
			Data: []any{map[string]any{"child_name": "产品A"}},
		}
		resp.StripInstanceScores()
		if m := resp.Data[0].(map[string]any); m["child_name"] != "产品A" {
			t.Errorf("业务字段被误删")
		}
	})

	t.Run("非 map 元素不影响", func(t *testing.T) {
		resp := &QueryObjectInstancesResp{Data: []any{"raw", 42}}
		resp.StripInstanceScores() // 不 panic 即可
		if len(resp.Data) != 2 {
			t.Fatalf("非 map 元素被改动")
		}
	})
}

// TestHasScoringOperator 覆盖 #236 的意图判定：knn / match 有真实相关度分要保留 _score，
// 纯结构化过滤无评分语义要剥除。filters 语法糖与 condition 树两条入口都要认。
func TestHasScoringOperator(t *testing.T) {
	tests := []struct {
		name string
		req  *QueryObjectInstancesReq
		want bool
	}{
		{"nil req", nil, false},
		{"空 req", &QueryObjectInstancesReq{}, false},
		{
			"filters 纯结构化",
			&QueryObjectInstancesReq{Filters: []FlatFilter{
				{Field: "child_name", Op: KnOperationTypeLike, Value: "%产品%"},
				{Field: "qty", Op: KnOperationTypeGreater, Value: 10},
			}},
			false,
		},
		{
			"filters 含 knn",
			&QueryObjectInstancesReq{Filters: []FlatFilter{
				{Field: "child_name", Op: KnOperationTypeKnn, Value: "产品"},
			}},
			true,
		},
		{
			"filters 含 match",
			&QueryObjectInstancesReq{Filters: []FlatFilter{
				{Field: "child_name", Op: KnOperationTypeMatch, Value: "产品"},
			}},
			true,
		},
		{
			"condition 纯结构化 AND",
			&QueryObjectInstancesReq{Cond: &KnCondition{
				Operation: KnOperationTypeAnd,
				SubConditions: []*KnCondition{
					{Field: "a", Operation: KnOperationTypeEqual, Value: 1},
					{Field: "b", Operation: KnOperationTypeIn, Value: []int{1, 2}},
				},
			}},
			false,
		},
		{
			"condition 嵌套里含 knn",
			&QueryObjectInstancesReq{Cond: &KnCondition{
				Operation: KnOperationTypeAnd,
				SubConditions: []*KnCondition{
					{Field: "a", Operation: KnOperationTypeEqual, Value: 1},
					{Field: "vec", Operation: KnOperationTypeKnn, Value: "x"},
				},
			}},
			true,
		},
		{
			"condition 深层嵌套含 match",
			&QueryObjectInstancesReq{Cond: &KnCondition{
				Operation: KnOperationTypeOr,
				SubConditions: []*KnCondition{
					{Operation: KnOperationTypeAnd, SubConditions: []*KnCondition{
						{Field: "c", Operation: KnOperationTypeMatch, Value: "y"},
					}},
				},
			}},
			true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.req.HasScoringOperator(); got != tc.want {
				t.Errorf("HasScoringOperator() = %v, want %v", got, tc.want)
			}
		})
	}
}
