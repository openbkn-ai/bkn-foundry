// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package querymodel

const (
	SQLReadRelation SQLReadKind = "relation"
	SQLReadPage     SQLReadKind = "page"
	SQLReadCount    SQLReadKind = "count"
)

const (
	CompletionWindowEnd   CompletionKind = "window_end"
	CompletionRelationEnd CompletionKind = "relation_end"
)

type SQLReadKind string

// SQLReadRequest 控制本次读取，不携带新的过滤、投影、身份或语义上下文。
// Offset 是逻辑查询结果内的读取位置，续页沿用上次返回的 NextOffset。
// Limit=nil 表示读取完整关系，非 nil 的 0 表示空窗口。
type SQLReadRequest struct {
	Kind   SQLReadKind
	Offset int64
	Limit  *int64
}

// Row 按 Schema 的列位置保存类型化值；SQL NULL 使用 Value{Data: NullValue{}}。
type Row []Value

type Batch struct {
	Rows []Row
}

type CompletionKind string

// SQLReadCompletion 仅在正常 io.EOF 后有效；源端错误由 Next 返回，不能伪装为完整结束。
type SQLReadCompletion struct {
	Kind       CompletionKind
	NextOffset *int64
	RowsRead   int64
}
