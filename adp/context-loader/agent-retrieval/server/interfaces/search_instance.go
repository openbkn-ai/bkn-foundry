// Copyright openbkn.ai
//
// Licensed under the OpenBKN License.
// See the LICENSE file in the project root for details.

package interfaces

// SearchInstanceReq search_instance request.
//
// Only three knobs are exposed that Agent can correctly judge: range (concept_groups), width (max_object_types),
// Depth(max_instances_per_type). Candidate pool size, vector sub-condition budget, correlation threshold and other operational levels.
// Parameters are not entered here - the Agent cannot determine how to adjust them. If you want to adjust them, go to kn_search's retrieval_config.
type SearchInstanceReq struct {
	XAccountID   string `header:"x-account-id"`
	XAccountType string `header:"x-account-type"`
	XKnID        string `header:"x-kn-id"`

	Query string `json:"query" validate:"required"`
	KnID  string `json:"kn_id,omitempty"`
	// ConceptGroups limits the concept groups participating in the recall, leaving it blank will result in the entire network.
	ConceptGroups []string `json:"concept_groups,omitempty"`
	// ObjectTypes pins recall to these object types (object type ids); empty means no limit.
	//
	// Ids only, never names: every tool that can hand an agent an object type returns its id
	// (search_schema.concept_id, search_instance.object_type_id, get_kn_detail.object_types[].id),
	// so accepting names buys nothing and costs the ability to tell "you passed a name" apart from
	// "this object type does not exist" when the list matches nothing.
	ObjectTypes []string `json:"object_types,omitempty"`
	// ExcludeObjectTypes drops these object types (object type ids) from recall. Exclusion wins over
	// ObjectTypes when an id appears in both.
	//
	// It sits next to the allow list because an agent can usually name what is noise -- the object
	// types the previous call returned and it did not want -- more precisely than what it wants.
	ExcludeObjectTypes []string `json:"exclude_object_types,omitempty"`
	// MaxObjectTypes is the upper limit of the number of object types participating in instance recall (Top-K of concept recall).
	MaxObjectTypes *int `json:"max_object_types,omitempty" default:"10"`
	// MaxInstancesPerType returns at most several instances of each object type.
	MaxInstancesPerType *int `json:"max_instances_per_type,omitempty" default:"5"`
	// IncludeObjectTypes controls whether to include a simplified definition of the hit object type. It is enabled by default.
	// Turning it off is only worthwhile if the caller already has the schema for these object types.
	IncludeObjectTypes *bool `json:"include_object_types,omitempty" default:"true"`

	// Rerank: Whether to perform cross-encoder refinement on the recall results. The default is off.
	//
	// Leave it to the caller rather than the deployer: only the person who initiated the query knows whether the query is more accurate or faster this time.
	// The cost is one more model call (about 100~400ms), and the rerank small model is required to be registered in the model factory;
	// When the model is unavailable, it automatically returns to the fusion sequence without reporting an error.
	//
	// There are only two gears: on/off. Shadow (adjusts the model but does not change the order, only records the sorting differences) is an operation and maintenance file used for evidence collection.
	// Go to kn_search's retrieval_config.semantic_instance_retrieval.instance_rerank_mode,
	// Do not put it in the tool parameter list - the Agent has no use for it.
	Rerank *bool `json:"rerank,omitempty" default:"false"`

	// IndexOpsOnly allows the attached condition_operations to retain only the operators brought by the index. Set by the MCP layer,
	// Without entering the request contract - the comparison operator can be deduced according to the attribute type, and issuing it one by one is pure noise to the Agent, and it is very expensive:
	// After actually measuring 154 attributes of a knowledge network, the total operator size is 15KB, leaving only 364 bytes for the index operator.
	// REST callers (direct consumers like Studio) still get the full amount.
	IndexOpsOnly bool `json:"-"`
}

// SearchInstanceResp search_instance response.
//
// Examples + field definitions required to understand these examples. The latter is included because a tool that is not self-contained will force the caller to go back and adjust it again.
// Search_schema / get_object_types once, and that time will recall the concept of the same query and run it again.
type SearchInstanceResp struct {
	Nodes []any `json:"nodes"`
	// ObjectTypes only contains object types that actually appear in Nodes (concise definition: attribute name and type),
	// Not all of them are scanned by concept recall - there are often dozens of them, and most of them have no examples.
	ObjectTypes []any `json:"object_types,omitempty"`
	// Message only appears if there are no hits, explaining why it is empty.
	Message string `json:"message,omitempty"`
}
