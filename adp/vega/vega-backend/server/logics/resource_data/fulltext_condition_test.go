// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package resource_data

import (
	"strings"
	"testing"

	"vega-backend/interfaces"
	"vega-backend/logics/filter_condition"
)

func tableResource(indexName string) *interfaces.Resource {
	status := interfaces.ResourceLocalIndexStatusUnavailable
	if indexName != "" {
		status = interfaces.ResourceLocalIndexStatusAvailable
	}
	return &interfaces.Resource{
		Name:             "yanfeng_kb.knowledge",
		Category:         interfaces.ResourceCategoryTable,
		LocalIndexStatus: status,
		LocalIndexName:   indexName,
	}
}

func Test_validateFulltextConditions(t *testing.T) {
	matchCond := &interfaces.FilterCondCfg{
		Operation: filter_condition.OperationAnd,
		SubConds: []*interfaces.FilterCondCfg{
			{Name: "content", Operation: filter_condition.OperationMatch},
		},
	}

	t.Run("表资源没有本地索引时拒绝全文条件并说明原因", func(t *testing.T) {
		err := validateFulltextConditions(tableResource(""), matchCond)
		if err == nil {
			t.Fatal("expected an error when the table resource has no local index")
		}
		// 报错必须指向真实原因（没建索引），而不是「算子不支持」——两者的下一步不同。
		for _, want := range []string{"content", "no local index", "yanfeng_kb.knowledge"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q should mention %q", err.Error(), want)
			}
		}
	})

	t.Run("表资源建过本地索引时放行", func(t *testing.T) {
		if err := validateFulltextConditions(tableResource("vega-build-res-task"), matchCond); err != nil {
			t.Fatalf("expected no error once the index exists, got %v", err)
		}
	})

	t.Run("过时索引不允许全文检索", func(t *testing.T) {
		resource := tableResource("vega-build-res-task")
		resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusStale
		if err := validateFulltextConditions(resource, matchCond); err == nil {
			t.Fatal("expected stale local index to reject full-text search")
		}
	})

	t.Run("索引类目资源不受此校验约束", func(t *testing.T) {
		res := &interfaces.Resource{Name: "idx", Category: interfaces.ResourceCategoryIndex}
		if err := validateFulltextConditions(res, matchCond); err != nil {
			t.Fatalf("index-category resources are natively full-text capable, got %v", err)
		}
	})

	t.Run("非全文算子不受影响", func(t *testing.T) {
		cfg := &interfaces.FilterCondCfg{
			Operation: filter_condition.OperationAnd,
			SubConds: []*interfaces.FilterCondCfg{
				{Name: "content", Operation: filter_condition.OperationLike},
			},
		}
		if err := validateFulltextConditions(tableResource(""), cfg); err != nil {
			t.Fatalf("like must stay usable without an index, got %v", err)
		}
	})

	t.Run("嵌套条件里的全文算子同样被发现", func(t *testing.T) {
		cfg := &interfaces.FilterCondCfg{
			Operation: filter_condition.OperationAnd,
			SubConds: []*interfaces.FilterCondCfg{
				{Operation: filter_condition.OperationOr, SubConds: []*interfaces.FilterCondCfg{
					{Name: "id", Operation: filter_condition.OperationEqual},
					{Name: "title", Operation: filter_condition.OperationMultiMatch},
				}},
			},
		}
		if err := validateFulltextConditions(tableResource(""), cfg); err == nil {
			t.Fatal("a full-text operation nested in sub-conditions must still be rejected")
		}
	})
}
