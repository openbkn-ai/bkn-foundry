// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package querymodel

import "time"

type PagingMode string

const (
	PagingSingle PagingMode = "single"
	PagingCursor PagingMode = "cursor"
)

type PagingOptions struct {
	Mode      PagingMode
	Offset    int64
	Limit     int64
	KeepAlive time.Duration
}

type BinaryReadMode string

const (
	BinaryNotRequested BinaryReadMode = "not_requested"
	BinaryMetadata     BinaryReadMode = "metadata"
	BinaryContent      BinaryReadMode = "content"
)

// ExecutionOptions 管理 API 读取选项，不替代 IR 内部定义关系的 Limit/TopK。
type ExecutionOptions struct {
	Paging           PagingOptions
	NeedTotal        bool
	Timeout          time.Duration
	QuerySource      QuerySource
	IgnoreLocalIndex bool
	BinaryMode       BinaryReadMode
}

// SQLPrepareRequest 直接引用完整的已绑定查询，语义、Schema、依赖及 slot 声明
// 均从 Query 读取，不在请求包装中重复保存。
type SQLPrepareRequest struct {
	Query         *BoundQuery
	TargetBinding TargetBinding
	Options       ExecutionOptions
}

type SQLReadKind string

const (
	SQLReadRelation SQLReadKind = "relation"
	SQLReadPage     SQLReadKind = "page"
	SQLReadCount    SQLReadKind = "count"
)

// SQLPreparedMetadata 只提供执行服务需要的查询描述。
// SQL 模板、参数布局、运行时参数检查和结果解码由 Connector 私有制品持有。
type SQLPreparedMetadata struct {
	TargetFingerprint string
	OutputSchema      Schema
	Dependencies      []Dependency
	Semantics         SemanticContext
	RuntimeSlots      []RuntimeSlot
}

// SQLQueryExecutionContext 在首次源请求（包括 count）前实例化，本次查询的重试和续页复用。
type SQLQueryExecutionContext struct {
	QueryID       string
	Identity      ExecutionIdentity
	StartedAt     time.Time
	RuntimeValues []RuntimeValue
}

// SQLReadRequest 控制本次读取，不携带新的过滤、投影、身份或语义上下文。
// Offset 是逻辑查询结果内的读取位置，续页沿用上次返回的 NextOffset。
// Limit=nil 表示读取完整关系，非 nil 的 0 表示空窗口。
type SQLReadRequest struct {
	Kind   SQLReadKind
	Offset int64
	Limit  *int64
}
