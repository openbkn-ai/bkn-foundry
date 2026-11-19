// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

// The words below are vocabulary the service reads business names by, in both languages it names
// ontology items in. They are data about names, not copy shown to readers.

// RelationCodeWords are the words that end the name of a field holding another record's code
// rather than naming a role: 物料编码, 客户ID, Supplier No., CustomerID. Longer words come first, so
// 编号 is taken before 号.
var RelationCodeWords = []string{"编码", "编号", "代码", "号码", "number", "code", "no.", "key", "id", "号"}

// DiagnosisCodeWords end or make up the name of a field that holds a code; Latin words match as a
// whole word or as the end of a name, others anywhere in it.
var DiagnosisCodeWords = []string{"编码", "编号", "代码", "号码", "号", "id", "code", "no", "number", "num", "key", "sn"}

// DiagnosisTypeWords name a field that tells which kind of record a code field refers to.
var DiagnosisTypeWords = []string{"类型", "类别", "种类", "type", "kind", "category"}

// DiagnosisParentWords name a field that refers to a record of the same object type above it.
var DiagnosisParentWords = []string{"上级", "父", "parent", "superior", "upper"}

// DiagnosisSourceWords name a field that refers to where a record comes from: its source document,
// its root demand, its parent. Such fields carry the document chain, so they are counted first.
var DiagnosisSourceWords = []string{"源单", "来源", "根", "上级", "父", "source", "root", "parent", "origin", "src"}

// DiagnosisSequenceWords name a field that numbers rows within a record or a table: a sequence or a
// line number, which matches small identities by chance and refers to nothing.
var DiagnosisSequenceWords = []string{"序号", "行号", "行序", "seq", "seqno", "lineno", "index", "idx", "entryid", "rowno"}

// DiagnosisDocumentNumberEnds end the name of a field that holds a document's number (预测单号,
// 订单编号, billno): a reference may point at it although rows of one document share it.
var DiagnosisDocumentNumberEnds = []string{"单号", "单编号", "单据编号", "billno", "billnumber"}
