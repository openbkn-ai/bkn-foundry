// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package querymodel

// 编译输入预算为固定常量，Parser、策略检查及后续绑定阶段统一复用。
// 调用方不传入或覆盖这些上限。
const (
	MaxSQLBytes = 1 << 20
	MaxTokens   = 10000
	MaxDepth    = 128
	MaxNodes    = 5000
	MaxINValues = 1000
)
