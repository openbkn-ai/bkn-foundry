// Copyright openbkn.ai
//
// Licensed under the OpenBKN License.
// See the LICENSE file in the project root for details.

package interfaces

// SearchInstanceReq search_instance request.
//
// 只暴露三个 Agent 能正确判断的旋钮：范围（concept_groups）、宽度（max_object_types）、
// 深度（max_instances_per_type）。候选池大小、向量子条件预算、相关性阈值一类运维级
// 参数不进这里——Agent 无从判断怎么调，要调的人走 kn_search 的 retrieval_config。
type SearchInstanceReq struct {
	XAccountID   string `header:"x-account-id"`
	XAccountType string `header:"x-account-type"`
	XKnID        string `header:"x-kn-id"`

	Query string `json:"query" validate:"required"`
	KnID  string `json:"kn_id,omitempty"`
	// ConceptGroups 限定参与召回的概念分组，留空则整网。
	ConceptGroups []string `json:"concept_groups,omitempty"`
	// MaxObjectTypes 参与实例召回的对象类数量上限（概念召回的 Top-K）。
	MaxObjectTypes *int `json:"max_object_types,omitempty" default:"10"`
	// MaxInstancesPerType 每个对象类最多返回几条实例。
	MaxInstancesPerType *int `json:"max_instances_per_type,omitempty" default:"5"`
}

// SearchInstanceResp search_instance response.
//
// 只回实例。字段含义走 search_schema / get_object_types，不在这里重复一份 schema。
type SearchInstanceResp struct {
	Nodes []any `json:"nodes"`
	// Message 仅在没有命中时出现，说明为什么是空的。
	Message string `json:"message,omitempty"`
}
