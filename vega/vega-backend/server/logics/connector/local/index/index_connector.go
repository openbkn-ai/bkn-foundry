// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package index provides shared metadata handling for index connectors.
package index

import (
	"fmt"

	"github.com/bytedance/sonic"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

// FieldMetadata 还原已加载资源的原生字段，不处理 alias、SubFields 寻址或能力规则。
func FieldMetadata(resource *interfaces.Resource) (map[string]interfaces.IndexFieldMeta, error) {
	if resource == nil || resource.SourceMetadata == nil {
		return nil, nil //nolint:nilnil // 历史资源缺少映射元数据是正常缺省状态。
	}
	raw, exists := resource.SourceMetadata["mapping"]
	if !exists {
		return nil, nil //nolint:nilnil // 历史资源缺少映射元数据是正常缺省状态。
	}
	fields, ok := raw.(map[string]interfaces.IndexFieldMeta)
	if !ok {
		encoded, err := sonic.Marshal(raw)
		if err != nil || sonic.Unmarshal(encoded, &fields) != nil {
			return nil, fmt.Errorf("invalid stored source index metadata")
		}
	}
	if fields == nil {
		return nil, fmt.Errorf("invalid stored source index metadata")
	}
	result := make(map[string]interfaces.IndexFieldMeta, len(fields))
	for name, field := range fields {
		result[name] = field
	}
	return result, nil
}
