// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package querymodel

// FieldID 标识关系中的一个输出槽。两次投影同一字段仍分配不同的输出 ID。
type FieldID uint64

type OutputEncodingKind string

const (
	EncodingScalar             OutputEncodingKind = "scalar"
	EncodingExactNumberString  OutputEncodingKind = "exact_number_string"
	EncodingExternalJSONString OutputEncodingKind = "external_json_string"
	EncodingDatasetJSON        OutputEncodingKind = "dataset_json"
	EncodingNativeOther        OutputEncodingKind = "native_other"
	EncodingBinaryMetadata     OutputEncodingKind = "binary_metadata"
	EncodingBinaryContent      OutputEncodingKind = "binary_content"
)

// OutputEncoding 只控制交付表示，不改变 LogicalType 的计算含义。
type OutputEncoding struct {
	Kind       OutputEncodingKind
	PublicType string
}

type OutputColumn struct {
	ID       FieldID
	Name     string
	Type     LogicalType
	Nullable bool
	Hidden   bool
	Lineage  []FieldID
	Encoding OutputEncoding
	Origin   Origin
}

// Schema 按列位置有序；Batch 中每行的单元格位置必须与其一致。
type Schema []OutputColumn

type FieldAddress struct {
	Relation RelationInstanceID
	Field    FieldID
}

type ColumnMapping struct {
	Input  FieldID
	Output FieldID
}
