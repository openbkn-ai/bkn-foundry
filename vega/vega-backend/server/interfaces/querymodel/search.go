// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package querymodel

type SearchKind string

const (
	SearchFullText SearchKind = "fulltext"
	SearchVector   SearchKind = "vector"
)

type VectorMetric string

const (
	MetricCosine VectorMetric = "cosine"
	MetricDot    VectorMetric = "dot_product"
	MetricL2     VectorMetric = "l2"
)

type SearchFilterStage string

const (
	FilterBeforeCandidates SearchFilterStage = "before_candidates"
	FilterAfterCandidates  SearchFilterStage = "after_candidates"
)

type TextMatchMode string

const (
	TextMatchAny    TextMatchMode = "any"
	TextMatchAll    TextMatchMode = "all"
	TextMatchPhrase TextMatchMode = "phrase"
)

type ModelBinding struct {
	ID         string
	Version    string
	Dimensions int
}

type SearchOptions struct {
	Kind          SearchKind
	TopK          int64
	Metric        VectorMetric
	Dimensions    int
	Model         ModelBinding
	TextMatch     TextMatchMode
	FilterStage   SearchFilterStage
	ScoreFunction FunctionID
}

// SearchArgument 用于结构化入口；文本向量化由受控服务完成，不在 Binder 内调用模型。
type SearchArgument struct {
	Text   *string
	Vector []float64
	Origin Origin
}

// Search 是未绑定检索关系；检索候选关系不能冒充底层资源的完整扫描。
type Search struct {
	Input     Relation
	Fields    []*Expr
	Query     SearchArgument
	Prefilter *Expr
	Options   SearchOptions
	Origin    Origin
}

func (*Search) queryRelation() {}
