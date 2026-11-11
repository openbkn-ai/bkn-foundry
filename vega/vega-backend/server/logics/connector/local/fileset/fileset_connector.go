// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package fileset provides shared metadata handling for fileset connectors.
package fileset

import (
	"fmt"

	"github.com/bytedance/sonic"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

// ColumnMetadata 还原已加载资源的原生列，不判断字段能力或修改资源。
func ColumnMetadata(resource *interfaces.Resource) (map[string]interfaces.FilesetColumnMeta, error) {
	if resource == nil || resource.SourceMetadata == nil {
		return nil, nil //nolint:nilnil // 历史资源缺少列元数据是正常缺省状态。
	}
	raw, exists := resource.SourceMetadata["columns"]
	if !exists {
		return nil, nil //nolint:nilnil // 历史资源缺少列元数据是正常缺省状态。
	}
	columns, ok := raw.([]interfaces.FilesetColumnMeta)
	if !ok {
		encoded, err := sonic.Marshal(raw)
		if err != nil || sonic.Unmarshal(encoded, &columns) != nil {
			return nil, fmt.Errorf("invalid stored source column metadata")
		}
	}
	if columns == nil {
		return nil, fmt.Errorf("invalid stored source column metadata")
	}
	result := make(map[string]interfaces.FilesetColumnMeta, len(columns))
	for _, column := range columns {
		result[column.Name] = column
	}
	return result, nil
}
