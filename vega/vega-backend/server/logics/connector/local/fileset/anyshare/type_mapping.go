// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package anyshare

import (
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

// DefaultFilterable 按 AnyShare 暴露的字段类型给出扫描默认值。
func DefaultFilterable(nativeType string) bool {
	switch nativeType {
	case interfaces.DataType_String, interfaces.DataType_Integer, interfaces.DataType_Boolean:
		return true
	default:
		return false
	}
}

// IsFilterable 委托文件集原生类型规则，不使用已有的用户开关。
func (c *AnyShareConnector) IsFilterable(column *interfaces.FilesetColumnMeta) bool {
	return column != nil && DefaultFilterable(column.Type)
}
