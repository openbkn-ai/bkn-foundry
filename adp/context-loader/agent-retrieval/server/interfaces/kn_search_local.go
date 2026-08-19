// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package interfaces defines interfaces for knowledge network search (local implementation)
package interfaces

import "context"

// ==================== Request Structures ====================

// KnSearchLocalRequest Knowledge network search local request.
type KnSearchLocalRequest struct {
	// Header Fields
	AccountID   string `json:"-" header:"x-account-id"`
	AccountType string `json:"-" header:"x-account-type"`

	// Request Body
	Query           string                   `json:"query" validate:"required"`
	KnID            string                   `json:"kn_id" validate:"required"`
	RetrievalConfig *KnSearchRetrievalConfig `json:"retrieval_config,omitempty"`
	OnlySchema      bool                     `json:"only_schema" default:"false"`
	EnableRerank    bool                     `json:"enable_rerank" default:"true"`
	// RerankModel overrides the relation fine-ranking small model; empty falls back
	// to the deployment default (config.concept_search_config.rerank_model).
	RerankModel string `json:"rerank_model,omitempty"`
	// IncludeColumns adds each data property's physical column name (mapped_field)
	// to the response for run_sql. Off by default to keep responses compact.
	IncludeColumns bool `json:"include_columns" default:"false"`
	// IndexOpsOnly See SearchSchemaReq field of the same name.
	IndexOpsOnly bool `json:"-"`
}

// KnSearchRetrievalConfig recall configuration parameters.
type KnSearchRetrievalConfig struct {
	ConceptRetrieval          *KnSearchConceptRetrievalConfig          `json:"concept_retrieval,omitempty"`
	SemanticInstanceRetrieval *KnSearchSemanticInstanceRetrievalConfig `json:"semantic_instance_retrieval,omitempty"`
	PropertyFilter            *KnSearchPropertyFilterConfig            `json:"property_filter,omitempty"`
}

// KnSearchConceptRetrievalConfig concept recall configuration parameters.
type KnSearchConceptRetrievalConfig struct {
	ConceptGroups []string `json:"concept_groups,omitempty"`
	// ObjectTypes / ExcludeObjectTypes scope recall to (or away from) the given object type ids.
	// Both are applied to the candidate pool *before* relevance ranking and the TopK cut: filtering
	// afterwards would silently drop a pinned object type that happened to rank below TopK.
	ObjectTypes            []string `json:"object_types,omitempty"`
	ExcludeObjectTypes     []string `json:"exclude_object_types,omitempty"`
	TopK                   int      `json:"top_k" default:"10"`
	IncludeSampleData      *bool    `json:"include_sample_data" default:"false"`
	SchemaBrief            *bool    `json:"schema_brief" default:"false"`
	EnableCoarseRecall     *bool    `json:"enable_coarse_recall" default:"true"`
	CoarseObjectLimit      int      `json:"coarse_object_limit" default:"2000"`
	CoarseRelationLimit    int      `json:"coarse_relation_limit" default:"300"`
	CoarseMinRelationCount int      `json:"coarse_min_relation_count" default:"5000"`
	EnablePropertyBrief    *bool    `json:"enable_property_brief" default:"true"`
	PerObjectPropertyTopK  int      `json:"per_object_property_top_k" default:"8"`
	GlobalPropertyTopK     int      `json:"global_property_top_k" default:"30"`
}

// KnSearchSemanticInstanceRetrievalConfig semantic instance recall configuration parameters.
type KnSearchSemanticInstanceRetrievalConfig struct {
	InitialCandidateCount             int     `json:"initial_candidate_count" default:"50"`
	PerTypeInstanceLimit              int     `json:"per_type_instance_limit" default:"5"`
	MaxSemanticSubConditions          int     `json:"max_semantic_sub_conditions" default:"10"`
	SemanticFieldKeepRatio            float64 `json:"semantic_field_keep_ratio" default:"0.2"`
	SemanticFieldKeepMin              int     `json:"semantic_field_keep_min" default:"5"`
	SemanticFieldKeepMax              int     `json:"semantic_field_keep_max" default:"15"`
	SemanticFieldRerankBatchSize      int     `json:"semantic_field_rerank_batch_size" default:"128"`
	MinDirectRelevance                float64 `json:"min_direct_relevance" default:"0.3"`
	EnableGlobalFinalScoreRatioFilter *bool   `json:"enable_global_final_score_ratio_filter" default:"true"`
	GlobalFinalScoreRatio             float64 `json:"global_final_score_ratio" default:"0.25"`
	ExactNameMatchScore               float64 `json:"exact_name_match_score" default:"0.85"`

	// EnableKnnInstanceRetrieval controls whether instance recall sends vector conditions. Close and leave only the full text:
	// Vectors can be recalled across languages and phrases, but each knn sub-condition must vectorize the query word once. This is.
	// The only part of the link where pay-per-view is also pay-per-view.
	EnableKnnInstanceRetrieval *bool `json:"enable_knn_instance_retrieval" default:"true"`
	// MaxKnnSubConditionsPerType limits how many vector conditions a single object type can send.
	// Multiple text fields in the same line each send knn once. The recall gain is very small, but the cost is linear superposition. By default, only.
	// The first field.
	MaxKnnSubConditionsPerType int `json:"max_knn_sub_conditions_per_type" default:"1"`

	// EnableRRFFusion controls whether instance recall splits knn and match into two queries, and then performs RRF fusion based on ranking.
	// Turn off the old path that returns a single OR query: the knn score (0~1) and BM25 score (no upper bound) on that path are.
	// OpenSearch adds directly, BM25 is always dominant, and vector hits cannot even enter the candidate set. Reserved only for escape doors.
	EnableRRFFusion *bool `json:"enable_rrf_fusion" default:"true"`
	// RRFK RRF fusion constant k: score = Σ 1/(k + rank). The larger k is, the smoother it is (between the top rankings of each channel.
	// The gap is reduced), 60 is a common value in literature and industry, and does not need to be readjusted across knowledge networks.
	RRFK int `json:"rrf_k" default:"60"`
	// KnnWeight: The weight of the vector channel in the fusion, 0~1, default 0.5 (equal weight, bit by bit equivalent to without weight).
	// The full text channel takes 1-KnnWeight: the absolute magnitude of the ranking does not affect the sorting, only the **proportion** of the two channels is meaningful.
	// So one number is enough and there is no need for two independent weights.
	//
	// 1 = only trust vectors, 0 = only trust full text. When is it worth biasing: BM25 word segmentation on Chinese short names and cross-language expressions.
	// Less effective, vectors are more reliable; the opposite is true for numbered/encoded fields.
	//
	// Cost: The partial weight will break the cross-object type anchor point of "the first position of any channel is always 1.0" - after increasing the vector weight,
	// Object classes without vector fields are suppressed overall. That is the **semantically correct** result of the caller's declared preference,
	// Not a bug, but because of this the default must remain 0.5.
	KnnWeight *float64 `json:"knn_weight,omitempty" default:"0.5"`

	// InstanceRerankMode fine ranking switch: off / shadow / on.
	//
	// The first-level RRF only reconciles rankings and cannot determine semantic differences such as "arrears" and "repayments", and rankings are not expressible.
	// There is no absolute correlation - the first number is always 1.0, even if it has no correlation. cross-encoder combines query with document.
	// Spelling the same sequence into attention is just to make up for this grid.
	//
	// The default is off: one more model call, the delay increases by 100~400ms, and it is normal that the reranker is not registered in the customer environment.
	// shadow returns the fusion sequence as usual, but adjusts the model once more and records the difference between the two sequences for pre-default evidence collection.
	InstanceRerankMode string `json:"instance_rerank_mode" default:"off"`
	// InstanceRerankModel overrides fine-ranking small model names; leave blank to use the default reranker configured by model management (#842).
	InstanceRerankModel string `json:"instance_rerank_model,omitempty"`
	// RerankTopN The number of candidates entering the refined ranking. Fine rowing is O(N) times forward and must have an upper bound.
	RerankTopN int `json:"rerank_top_n" default:"50"`
	// RerankFieldCharLimit The truncated length of a single field into refined text.
	// mf-model-api will silently cut a single document to 4000 characters. If there is no limit on long fields, the tail fields will not participate in scoring.
	RerankFieldCharLimit int `json:"rerank_field_char_limit" default:"200"`
}

// KnSearchPropertyFilterConfig instancepropertyfilterconfiguration.
type KnSearchPropertyFilterConfig struct {
	MaxPropertiesPerInstance int   `json:"max_properties_per_instance" default:"20"`
	MaxPropertyValueLength   int   `json:"max_property_value_length" default:"500"`
	EnablePropertyFilter     *bool `json:"enable_property_filter" default:"true"`
}

// ==================== Response Structures ====================

// KnSearchLocalResponse Knowledge network retrieves local response.
type KnSearchLocalResponse struct {
	ObjectTypes   []*KnSearchObjectType   `json:"object_types,omitempty"`
	RelationTypes []*KnSearchRelationType `json:"relation_types,omitempty"`
	ActionTypes   []*KnSearchActionType   `json:"action_types,omitempty"`
	Nodes         []*KnSearchNode         `json:"nodes,omitempty"`
	Message       string                  `json:"message,omitempty"`
}

// KnSearchObjectType object type (local response shape)
type KnSearchObjectType struct {
	ConceptType     string                   `json:"concept_type,omitempty"`
	ConceptID       string                   `json:"concept_id"`
	ConceptName     string                   `json:"concept_name"`
	Comment         string                   `json:"comment,omitempty"`
	Tags            []string                 `json:"tags,omitempty"`
	DataSource      *ResourceInfo            `json:"data_source,omitempty"`
	DataProperties  []*KnSearchDataProperty  `json:"data_properties,omitempty"`
	LogicProperties []*KnSearchLogicProperty `json:"logic_properties,omitempty"`
	PrimaryKeys     []string                 `json:"primary_keys,omitempty"`
	SampleData      map[string]any           `json:"sample_data,omitempty"`
}

// KnSearchDataProperty data property (local response shape)
type KnSearchDataProperty struct {
	Name string `json:"name,omitempty"`
	// Column is the physical column name (from mapped_field) to use when writing
	// run_sql against the object type's data resource. It can differ from Name
	// (logical), and one resource may back several object types with different
	// logical names. Only populated when the request sets include_columns=true.
	Column              string            `json:"column,omitempty"`
	Comment             string            `json:"comment,omitempty"`
	Type                string            `json:"type,omitempty"`
	ConditionOperations []KnOperationType `json:"condition_operations,omitempty"`
}

// KnSearchLogicProperty logic property (local response shape)
type KnSearchLogicProperty struct {
	Name       string              `json:"name,omitempty"`
	Comment    string              `json:"comment,omitempty"`
	Type       string              `json:"type,omitempty"`
	DataSource map[string]any      `json:"data_source,omitempty"`
	Parameters []PropertyParameter `json:"parameters,omitempty"`
}

// KnSearchRelationType relation type (local response shape)
type KnSearchRelationType struct {
	ConceptType        string `json:"concept_type,omitempty"`
	ConceptID          string `json:"concept_id"`
	ConceptName        string `json:"concept_name"`
	Comment            string `json:"comment,omitempty"`
	SourceObjectTypeID string `json:"source_object_type_id"`
	TargetObjectTypeID string `json:"target_object_type_id"`
}

// KnSearchActionType action type (local response shape)
type KnSearchActionType struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	ObjectTypeID   string   `json:"object_type_id"`
	ObjectTypeName string   `json:"object_type_name"`
	Comment        string   `json:"comment,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	KnID           string   `json:"kn_id"`
}

// KnSearchNode node (semantic instance) in local response
type KnSearchNode struct {
	ObjectTypeID     string         `json:"object_type_id"`
	ObjectTypeName   string         `json:"object_type_name,omitempty"`
	InstanceName     string         `json:"instance_name,omitempty"`
	UniqueIdentities map[string]any `json:"unique_identities,omitempty"`
	Properties       map[string]any `json:"properties,omitempty"`
	// Score relevance score. **Without omitempty**: 0 points is a meaningful value (local scoring does not allow overlap.
	// When it is 0), omitting a field will make it difficult for the caller to distinguish between "there is no such field" and "when it is 0".
	Score float64 `json:"score"`
	// RecallScore retains the original _score (OpenSearch relevance) of the recall phase. Score on RRF path.
	// It will be covered by fusion points. The two dimensions are different and need to be visible at the same time when troubleshooting.
	RecallScore float64 `json:"recall_score,omitempty"`
	// RerankScore Relevance score given by cross-encoder. Score is it when mode=on;
	// When mode=shadow, Score is still a fusion score, and this field is brought out separately for comparison.
	RerankScore float64 `json:"rerank_score,omitempty"`
}

// ==================== Internal Structures ====================

// KnSearchConceptResult concept retrieval result (internal)
type KnSearchConceptResult struct {
	ObjectTypes   []*KnSearchObjectType
	RelationTypes []*KnSearchRelationType
	ActionTypes   []*KnSearchActionType
	// UnmatchedObjectTypes lists the ids from the caller's object_types allow list that match no
	// object type in this knowledge network. Carried out of concept retrieval so the caller can be
	// told "these ids do not exist" instead of being handed a bare empty result.
	UnmatchedObjectTypes []string
}

// KnSearchSemanticInstanceResult semantic instance retrieval result (internal)
type KnSearchSemanticInstanceResult struct {
	Nodes   []*KnSearchNode
	Message string
}

// ==================== Service Interface ====================

// IKnSearchLocalService kn_search local service interface
type IKnSearchLocalService interface {
	// Search Knowledge network retrieval (local implementation)
	Search(ctx context.Context, req *KnSearchLocalRequest) (*KnSearchLocalResponse, error)
}
