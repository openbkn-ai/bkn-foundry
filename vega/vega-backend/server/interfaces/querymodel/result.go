// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package querymodel

type CellState string

const (
	CellValue          CellState = "value"
	CellMissing        CellState = "missing"
	CellUnavailable    CellState = "unavailable"
	CellBinaryMetadata CellState = "binary_metadata"
)

type UnavailableReason string

const (
	UnavailableNotRequested UnavailableReason = "not_requested"
	UnavailableNotStored    UnavailableReason = "not_stored"
)

type BinaryMetadataValue struct {
	Length int64
}

// ResultCell 的 SQL NULL 是 CellValue + Value{Data: NullValue{}}。
// missing/unavailable 和仅供展示的 BinaryMetadata 都不是完整计算值。
// CellBinaryMetadata 只设置 BinaryMetadata，不伪造 Binary 内容或 NULL。
type ResultCell struct {
	State          CellState
	Value          *Value
	Unavailable    UnavailableReason
	BinaryMetadata *BinaryMetadataValue
}

type Row []ResultCell

type Batch struct {
	Rows []Row
}

type CompletionKind string

const (
	CompletionWindowEnd     CompletionKind = "window_end"
	CompletionRelationEnd   CompletionKind = "relation_end"
	CompletionResourceLimit CompletionKind = "resource_limit"
)

type ResourceLimitKind string

const (
	ResourceLimitBytes        ResourceLimitKind = "bytes"
	ResourceLimitTime         ResourceLimitKind = "time"
	ResourceLimitSourceWindow ResourceLimitKind = "source_window"
)

// SQLReadCompletion 仅在正常 io.EOF 后有效。ResourceLimit 不代表完整关系结束，
// 源端错误必须由 Next 返回错误，不能包装为 EOF 或成功截断。
type SQLReadCompletion struct {
	Kind        CompletionKind
	NextOffset  *int64
	RowsRead    int64
	BytesRead   int64
	LimitReason ResourceLimitKind
}
