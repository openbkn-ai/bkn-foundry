// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "context"

// KnConceptType Knowledge Network Concept Type
type KnConceptType string

const (
	KnConceptTypeObject   KnConceptType = "object_type"   // Object Type
	KnConceptTypeRelation KnConceptType = "relation_type" // Relation Type
	KnConceptTypeAction   KnConceptType = "action_type"   // Action Type
)

// QueryObjectInstancesReq Request object for querying detailed object instances
type QueryObjectInstancesReq struct {
	KnID               string       `form:"kn_id"`                // Knowledge Network ID
	OtID               string       `form:"ot_id"`                // Object Type ID
	IncludeTypeInfo    bool         `form:"include_type_info"`    // Whether to include object type info
	IncludeLogicParams bool         `form:"include_logic_params"` // Include calculation parameters for logic properties, default false
	Cond               *KnCondition `json:"condition"`            // Retrieval conditions
	// Filters is a flat shortcut for the common "field op value [AND ...]" case.
	// When set and Cond is empty, the driven adapter AND-combines them into Cond
	// (value_from defaults to const). Mutually exclusive with condition; condition
	// wins if both are provided.
	Filters    []FlatFilter `json:"filters,omitempty"`
	Limit      int          `json:"limit" validate:"min=1,max=10000" default:"10"` // Quantity limit, default 10, range 1-10000
	Properties []string     `json:"properties"`                                    // Specify the returned object attribute field list. By default, all attributes are returned.
	// Sort sort field. Downstream ObjectQueryBaseOnObjectType.Sort is an array (can be sorted by multiple fields),
	// If not passed, the downstream will sort by default (object index path is _score + primary key, resource path is @timestamp desc).
	// Whether the field exists in the object type is verified by the downstream, and it is only transparently transmitted here.
	Sort []*SortSpec `json:"sort,omitempty"`
	// NeedTotal lets the downstream backfill total_count. Set true unconditionally by driven adapter and not open to the public:
	// Without it, the caller only knows "Is there a next page?" but does not know the total number of hits and cannot judge the scale of the result.
	NeedTotal bool `json:"need_total"`
	// SearchAfter cursor paging: Pass in the search_after returned by the previous page response, which is used to sequentially pull the next page; leave it blank for the first query.
	// Applicable to object index/data view path (turn forward without page jump).
	SearchAfter []any `json:"search_after,omitempty"`
	// Offset offset paging: applicable to resource (vega table source) path, supports jumping to any page; mutually exclusive with search_after.
	Offset int `json:"offset,omitempty"`

	// The following two items are downstream query parameters rather than request body fields, so they are marked json: "-": the entire req structure will be.
	// Directly serialize the request body and send it to ontology-query. Missing tags will mix them into the body.
	// Both are only used by service internal callers and do not enter the MCP tool schema - see field comments.

	// ExcludeSystemProperties trims the system fields in the returned instance, optional value _instance_id /.
	// _instance_identity/_display. These three fields are pure context overhead during batch recall, but which ones can be lost?.
	// It depends on whether the caller wants to use them for drill-down later, and whether it should be left to the model to judge.
	ExcludeSystemProperties []string `json:"-" form:"exclude_system_properties"`
	// IgnoringStoreCache skips the index query and goes directly to the data source. Escape channel when index is stale or abnormal,
	// The price is an order of magnitude slower; exposing it to models can be abused as "try again".
	IgnoringStoreCache bool `json:"-" form:"ignoring_store_cache"`
}

// SortSpec is a single sort field. Same shape as downstream interfaces.SortParams, direction is asc / desc.
//
// The legality of the **value** of field and direction is verified by the downstream validateObjectSearchRequest and returns 400——.
// Only the downstream knows whether the field exists in the object type. Doing half the verification here will only cause the rules on both sides to drift.
// But **structural** nil elements must be blocked at this level (see rejectNilSortEntries): downstream validate.go and.
// logics/common.go all directly takes sp.Field, nil elements will not be replaced with 400 but a null pointer panic.
type SortSpec struct {
	Field     string `json:"field"`
	Direction string `json:"direction"`
}

// FlatFilter is a single field-op-value comparison used by
// QueryObjectInstancesReq.Filters. Multiple filters are AND-combined into a
// condition by the driven adapter.
type FlatFilter struct {
	Field string          `json:"field"` // Object type property name
	Op    KnOperationType `json:"op"`    // Comparison operator
	Value any             `json:"value"` // Field value (array for in/not_in)
}

type QueryObjectInstancesResp struct {
	Data          []any          `json:"datas"`                 // List of object instances
	ObjectConcept map[string]any `json:"object_type,omitempty"` // Object type definition, controlled by req.include_type_info whether to return.
	// TotalCount The total number of instances that meet the filter conditions, not limited by limit.
	//
	// Pointer + omitempty, three-state:
	// Valid and > 0 - true total.
	// Has value and = 0 - true zero hit (omitempty only swallows nil for pointers, not 0)
	// Missing field - not counted downstream, not a zero hit.
	//
	// The third state must be retained: although driven adapter is fixed with need_total, the downstream.
	// (BuildDslQuery of ontology-query logics/common.go) when search_after is not empty.
	// Will force NeedTotal=false, that is, the total will not be calculated at all from the second page of the cursor. At this time, if according to the value type.
	// Serializing to 0 is to use "uncalculated" as "zero hit", and it will be inconsistent with non-empty datas.
	TotalCount *int64 `json:"total_count,omitempty"`
	// SearchAfter next page cursor: If it is not empty, pass it in as the search_after of the next request to get the next page; if it is empty, it means there is no more data.
	SearchAfter []any `json:"search_after,omitempty"`
}

// StripInstanceScores removes the _score field from each object instance result.
//
// It should only be called when this query is purely structured filtering (without knn / match scoring operator): this type of query is.
// OpenSearch is implemented into constant scoring queries such as term/terms/range/prefix/regexp/exists, without filtering.
// Degenerates into match_all. Both assign the same constant score (usually 1.0) to each hit. There is no relevance ranking semantics.
// Exposing it to the caller will only mislead it into thinking that the results are sorted by relevance (see #236).
// knn / full-text match will score the true correlation score one by one, and it cannot be stripped at this time - used by the caller.
// HasScoringOperator decides whether to call this method after judgment.
func (r *QueryObjectInstancesResp) StripInstanceScores() {
	if r == nil {
		return
	}
	for _, item := range r.Data {
		if m, ok := item.(map[string]any); ok {
			delete(m, "_score")
		}
	}
}

// scoringOperators is a set of operators that will produce true correlation scores (different one by one); the remaining structured operators are in.
// The OpenSearch side is a constant score, and _score has no sorting semantics.
var scoringOperators = map[KnOperationType]struct{}{
	KnOperationTypeKnn:   {}, // Vector nearest neighbor, scored by similarity.
	KnOperationTypeMatch: {}, // Full text matching, scoring based on relevance.
}

// HasScoringOperator determines whether this query uses an operator (knn / match) that will generate a correlation score.
// Used to decide whether the response should retain _score (#236). Will check both filters syntactic sugar and expanded.
// condition tree, so the calling time is irrelevant (correct before and after expansion).
func (r *QueryObjectInstancesReq) HasScoringOperator() bool {
	if r == nil {
		return false
	}
	for _, f := range r.Filters {
		if _, ok := scoringOperators[f.Op]; ok {
			return true
		}
	}
	return condHasScoringOperator(r.Cond)
}

// condHasScoringOperator recursively determines whether the condition tree contains a scoring operator.
func condHasScoringOperator(c *KnCondition) bool {
	if c == nil {
		return false
	}
	if _, ok := scoringOperators[c.Operation]; ok {
		return true
	}
	for _, sub := range c.SubConditions {
		if condHasScoringOperator(sub) {
			return true
		}
	}
	return false
}

// QueryLogicPropertiesReq Request for querying logic properties values
type QueryLogicPropertiesReq struct {
	KnID               string                   `json:"kn_id"`
	OtID               string                   `json:"ot_id"`
	InstanceIdentities []map[string]interface{} `json:"_instance_identities"`
	Properties         []string                 `json:"properties"`
	DynamicParams      map[string]interface{}   `json:"dynamic_params"`
}

// QueryLogicPropertiesResp Response for querying logic properties values
type QueryLogicPropertiesResp struct {
	Datas []map[string]interface{} `json:"datas"`
}

// QueryInstanceSubgraphReq Subgraph query request
type QueryInstanceSubgraphReq struct {
	// Path parameters
	KnID string `form:"kn_id"`

	// Query parameters
	IncludeLogicParams bool `form:"include_logic_params"`

	// Body parameters - use interface{} to avoid explicit struct definition
	// Corresponds to SubGraphQueryBaseOnTypePath struct in ontology-query interface
	RelationTypePaths interface{} `json:"relation_type_paths"`
}

// QueryInstanceSubgraphResp Subgraph query response
type QueryInstanceSubgraphResp struct {
	// Use interface{} to directly return the original structure from the underlying interface
	// Corresponds to PathEntries struct in ontology-query interface
	Entries interface{} `json:"entries"`
}

// ExploreSubgraphReq is the starting point for exploratory subgraph query, corresponding to the downstream SubGraphQueryBaseOnSource.
// (`POST /subgraph` and leave query_type blank).
//
// It is complementary to the path template mode of QueryInstanceSubgraphReq and does not replace each other: the caller is required to first convert the entire.
// The object type sequence and relation type sequence of the path are spelled out, which is suitable for "knowing the topology and taking numbers in batches"; here only the starting point object type is required.
// + direction + number of hops, suitable for "unknown topology and need to discover correlations" - the latter is the default question form of the Agent.
type ExploreSubgraphReq struct {
	// The following fields go to the URL and not to the request body: the entire structure will be directly serialized into a body and sent to the downstream.
	KnID               string `json:"-" form:"kn_id"`
	IncludeLogicParams bool   `json:"-" form:"include_logic_params"`
	// ExcludeSystemProperties / IgnoringStoreCache is only used by service internal callers and does not enter MCP.
	// Tool schema, rationale is consistent with the field of the same name on QueryObjectInstancesReq.
	//
	// Regarding whether ExcludeSystemProperties takes effect in this interface: the downstream does query the nested starting point object.
	// This line of assignment in (startObjectQuery) is commented out, but that does not mean that the parameters are invalid - the system fields of the subgraph.
	// Generated by the sublayer itself rather than brought out by the starting point query (the reason is written next to the line of comments), the clipping occurs in.
	// When expandObjectPathsBatch assembles ObjectInfoInSubgraph, it reads exactly.
	// query.ExcludeSystemProperties. So pass through as usual.
	ExcludeSystemProperties []string `json:"-" form:"exclude_system_properties"`
	IgnoringStoreCache      bool     `json:"-" form:"ignoring_store_cache"`

	// SourceObjectTypeID The object type of the starting point for exploration.
	SourceObjectTypeID string `json:"source_object_type_id"`
	// Direction is the exploration direction, choose forward / backward / bidirectional, and is verified by the downstream.
	Direction string `json:"direction"`
	// PathLength is the maximum number of hops, ranging from 1 to 3.
	//
	// The upper bound is consistent with the downstream validateSubgraphSearchRequest (exceeding 3 times and returning 400); **The lower bound must be nailed here**:
	// 0 is an int zero value, which is indistinguishable from "not passed". However, the downstream does not report an error for 0 and only returns an empty subgraph - the caller will.
	// "Parameter missing" is read as "Nothing is connected". The verification is hung on the structure instead of each entry, REST and MCP.
	// They share the same rule (there is another explicit check on the MCP side to point out the field names).
	PathLength int `json:"path_length" validate:"min=1,max=3"`
	// ConceptGroups delineates the scope of exploration by concept grouping, and there is no limit if it is not passed on.
	ConceptGroups []string `json:"concept_groups,omitempty"`
	// The filter condition of Cond starting point object type is isomorphic to the condition of query_object_instance.
	Cond *KnCondition `json:"condition,omitempty"`
	// IncludeIncompletePath whether to return the incomplete path that "has traveled at least one edge but the type path has not been completed",
	// Default false. Zero-edge paths are not returned under any circumstances.
	IncludeIncompletePath bool `json:"include_incomplete_path,omitempty"`

	// The following four items act on the **starting object type**, not the entire subgraph: downstream SubGraphQueryBaseOnSource.
	// Embedded PageQuery, paging and sorting only touch the starting point collection.
	Sort        []*SortSpec `json:"sort,omitempty"`
	Limit       int         `json:"limit" validate:"min=1,max=10000" default:"10"`
	Offset      int         `json:"offset,omitempty"`
	SearchAfter []any       `json:"search_after,omitempty"`
	// NeedTotal is set to true unconditionally by the driven adapter and is not open to the public. The reason is the same as object query.
	NeedTotal bool `json:"need_total"`
}

// ExploreSubgraphResp corresponds to the downstream ObjectSubGraph.
//
// Note its relationship to QueryInstanceSubgraphResp: the downstream PathsEntries are.
// `{ entries: []ObjectSubGraph }`, that is, the path template mode returns an **array** of this structure, and the exploration mode.
// Returns a single. The two elements are of the same type, the only difference is "one" or "a group".
type ExploreSubgraphResp struct {
	// Objects are objects participating in the relationship, and key is the object id.
	Objects map[string]any `json:"objects"`
	// IsolatedObjects Isolated objects that have no relationship with other objects. **This is a valid conclusion and not an empty result**:
	// It clearly answers "There is no connection between/outward from these starting points".
	IsolatedObjects map[string]any `json:"isolated_objects,omitempty"`
	// RelationPaths A specific collection of relationship paths.
	RelationPaths []any `json:"relation_paths"`
	// TotalCount The total number of hits for the starting object type, three-state semantics and QueryObjectInstancesResp.TotalCount.
	// Completely consistent (value >0 / value =0 / missing means not counted), see resolveAbsentTotal.
	TotalCount *int64 `json:"total_count,omitempty"`
	// SearchAfter The next page cursor of the starting point object type.
	SearchAfter []any `json:"search_after,omitempty"`
	// CurrentPathNumber The current path number of downstream backfill.
	CurrentPathNumber int `json:"current_path_number,omitempty"`
}

// DrivenOntologyQuery Ontology query interface
type DrivenOntologyQuery interface {
	// QueryObjectInstances retrieves detailed data of objects for a specified object type
	QueryObjectInstances(ctx context.Context, req *QueryObjectInstancesReq) (resp *QueryObjectInstancesResp, err error)
	// QueryLogicProperties queries logic property values
	QueryLogicProperties(ctx context.Context, req *QueryLogicPropertiesReq) (resp *QueryLogicPropertiesResp, err error)
	// QueryActions queries actions
	QueryActions(ctx context.Context, req *QueryActionsRequest) (resp *QueryActionsResponse, err error)
	// ExecuteActions executes an action type (async), returning an execution id
	ExecuteActions(ctx context.Context, req *ExecuteActionsRequest) (resp *ExecuteActionsResponse, err error)
	// GetActionExecution retrieves a single execution's status and results by execution id
	GetActionExecution(ctx context.Context, req *GetActionExecutionRequest) (resp map[string]any, err error)
	// ListActionExecutions lists action execution history with optional filters and pagination
	ListActionExecutions(ctx context.Context, req *ListActionExecutionsRequest) (resp map[string]any, err error)
	// QueryInstanceSubgraph queries object subgraph along caller-supplied type paths
	QueryInstanceSubgraph(ctx context.Context, req *QueryInstanceSubgraphReq) (resp *QueryInstanceSubgraphResp, err error)
	// ExploreSubgraph explores an object subgraph from a source object type, by
	// direction and hop count, without the caller having to know the topology
	ExploreSubgraph(ctx context.Context, req *ExploreSubgraphReq) (resp *ExploreSubgraphResp, err error)
	// QueryMetricData computes one metric by its own definition
	// (POST .../metrics/{metric_id}/data)
	QueryMetricData(ctx context.Context, knID, metricID string, fillNull bool,
		req *MetricQueryDownstreamReq) (resp *MetricQueryDownstreamResp, err error)
}
