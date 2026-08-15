// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package resource_data

import (
	"fmt"

	"vega-backend/interfaces"
	"vega-backend/logics/filter_condition"
)

// validateFulltextConditions 在表资源没有本地索引时拒绝全文检索条件。
//
// 表资源建过本地索引就会自动转向索引查询（见 query 里的 LocalIndexName 分支），
// match 一类算子因此可用；没建索引则落回 connector 的 SQL，那边不可能有全文能力。
// 不在这里拦，请求会一路走到 SQL 生成器的 default 分支才炸，报出来的是「算子不
// 支持」——听上去像这个能力不存在，而真实原因是索引还没建。两者的下一步完全不同：
// 前者要改查询，后者要建索引。
//
// 向量检索不走这里：resolveVectorConditions 已经在字段解析阶段给出了更精确的判断
// （某个字段有没有向量特性），本函数只补全文这一半。
func validateFulltextConditions(resource *interfaces.Resource, cfg *interfaces.FilterCondCfg) error {
	if resource == nil || cfg == nil {
		return nil
	}
	// 索引类目与 dataset 本身就是 OpenSearch 存储，不依赖构建任务产出的本地索引。
	if resource.Category != interfaces.ResourceCategoryTable || resource.LocalIndexName != "" {
		return nil
	}
	return rejectFulltext(resource, cfg)
}

func rejectFulltext(resource *interfaces.Resource, cfg *interfaces.FilterCondCfg) error {
	if cfg == nil {
		return nil
	}
	for _, sub := range cfg.SubConds {
		if err := rejectFulltext(resource, sub); err != nil {
			return err
		}
	}
	if !filter_condition.IsFulltextOperation(cfg.Operation) {
		return nil
	}
	return fmt.Errorf("condition [%s] on field %q: resource %q has no local index; build one before full-text search",
		cfg.Operation, cfg.Name, resource.Name)
}
