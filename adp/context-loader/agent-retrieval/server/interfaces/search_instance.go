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
	// IncludeObjectTypes 控制是否附带命中对象类的精简定义，默认开。
	// 关掉只在「调用方已经拿着这些对象类的 Schema」时才划算。
	IncludeObjectTypes *bool `json:"include_object_types,omitempty" default:"true"`

	// Rerank 是否对召回结果做 cross-encoder 精排，默认关。
	//
	// 交给调用方而不是部署方：这一次查询要精度还是要速度，只有发起查询的人知道。
	// 代价是多一次模型调用（约 100~400ms），且要求模型工厂里注册了 rerank 小模型；
	// 模型不可用时自动退回融合序，不报错。
	//
	// 只有开/关两档。shadow（调模型但不改序、只记录排序差异）是取证用的运维档，
	// 走 kn_search 的 retrieval_config.semantic_instance_retrieval.instance_rerank_mode，
	// 不放进工具参数表——Agent 拿它没有用处。
	Rerank *bool `json:"rerank,omitempty" default:"false"`

	// IndexOpsOnly 让附带的 condition_operations 只保留索引带来的算子。由 MCP 层设置，
	// 不进请求契约——比较算子按属性 type 可推导，逐个下发对 Agent 是纯噪音，而且很贵：
	// 实测一个知识网络的 154 个属性，全量算子 15KB，只留索引算子 364 字节。
	// REST 调用方（Studio 这类直连消费者）仍拿全量。
	IndexOpsOnly bool `json:"-"`
}

// SearchInstanceResp search_instance response.
//
// 实例 + 读懂这些实例所需的字段定义。带上后者是因为不自足的工具会逼调用方回头再调
// 一次 search_schema / get_object_types，而那一趟会把同一个 query 的概念召回重跑一遍。
type SearchInstanceResp struct {
	Nodes []any `json:"nodes"`
	// ObjectTypes 只含 Nodes 里真正出现过的对象类（精简定义：属性名与类型），
	// 不是概念召回扫过的全部——后者动辄几十个，绝大多数没有出实例。
	ObjectTypes []any `json:"object_types,omitempty"`
	// Message 仅在没有命中时出现，说明为什么是空的。
	Message string `json:"message,omitempty"`
}
