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
	Properties []string     `json:"properties"`                                    // 指定返回的对象属性字段列表，默认返回所有属性
	// Sort 排序字段。下游 ObjectQueryBaseOnObjectType.Sort 是数组（可多字段排序），
	// 不传则由下游按默认排序（对象索引路径为 _score + 主键，资源路径为 @timestamp desc）。
	// field 是否存在于对象类由下游校验，此处只透传。
	Sort []*SortSpec `json:"sort,omitempty"`
	// NeedTotal 让下游回填 total_count。由 driven adapter 无条件置 true，不对外开放：
	// 缺了它调用方只知道「还有没有下一页」，不知道命中总量，无从判断结果规模。
	NeedTotal bool `json:"need_total"`
	// SearchAfter 游标分页：传入上一页响应返回的 search_after，用于顺序拉取下一页；首次查询留空。
	// 适用于对象索引 / 数据视图路径（顺翻，不跳页）。
	SearchAfter []any `json:"search_after,omitempty"`
	// Offset 偏移翻页：适用于资源（vega 表源）路径，支持跳到任意页；与 search_after 互斥。
	Offset int `json:"offset,omitempty"`

	// 以下两项是下游的 query 参数而非请求体字段，故标 json:"-"：整个 req 结构体会被
	// 直接序列化成请求体发给 ontology-query，漏标会把它们混进 body。
	// 两者都只供服务内部调用方使用，不进 MCP 工具 schema——见字段注释。

	// ExcludeSystemProperties 裁剪返回实例里的系统字段，可选值 _instance_id /
	// _instance_identity / _display。批量召回时这三个字段是纯 context 开销，但哪些能丢
	// 取决于调用方后续要不要拿它们做下钻，不该交给模型判断。
	ExcludeSystemProperties []string `json:"-" form:"exclude_system_properties"`
	// IgnoringStoreCache 跳过索引查询直接走数据源。索引陈旧或异常时的逃生通道，
	// 代价是慢一个数量级；暴露给模型会被当成「重试一次」滥用。
	IgnoringStoreCache bool `json:"-" form:"ignoring_store_cache"`
}

// SortSpec 是单个排序字段。与下游 interfaces.SortParams 同形，direction 取 asc / desc。
//
// field 与 direction 的**取值**合法性由下游 validateObjectSearchRequest 校验并回 400——
// 字段是否存在于对象类只有下游知道，在这里做一半校验只会让两侧规则漂移。
// 但**结构性** nil 元素必须在本层拦（见 rejectNilSortEntries）：下游 validate.go 与
// logics/common.go 都直接取 sp.Field，nil 元素不会换来 400 而是空指针 panic。
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
	ObjectConcept map[string]any `json:"object_type,omitempty"` // Object type definition，由 req.include_type_info 控制是否返回
	// TotalCount 满足过滤条件的实例总数，不受 limit 限制。
	//
	// 指针 + omitempty，三态：
	//   有值且 > 0 —— 真实总数
	//   有值且 = 0 —— 真实零命中（omitempty 对指针只吞 nil，不吞 0）
	//   字段缺失   —— 下游没算，不是零命中
	//
	// 第三态是必须留的：driven adapter 虽然固定开 need_total，但下游
	// （ontology-query logics/common.go 的 BuildDslQuery）在 search_after 非空时
	// 会强制 NeedTotal=false，即游标翻页第二页起根本不计算总数。此时若按值类型
	// 序列化成 0，就是拿「未计算」冒充「零命中」，而且会和非空 datas 自相矛盾。
	TotalCount *int64 `json:"total_count,omitempty"`
	// SearchAfter 下一页游标：非空时把它作为下次请求的 search_after 传入以取下一页；为空表示无更多数据。
	SearchAfter []any `json:"search_after,omitempty"`
}

// StripInstanceScores 删除每条对象实例结果里的 _score 字段。
//
// 仅当本次查询是纯结构化过滤（无 knn / match 打分算子）时才应调用：这类查询在
// OpenSearch 侧落成 term/terms/range/prefix/regexp/exists 等常量打分查询，无过滤时
// 退化为 match_all，二者都给每条命中赋同一个常量分（通常 1.0），不存在相关度排序语义，
// 透给调用方只会误导其以为结果按相关度排序（见 #236）。
// knn / 全文 match 会逐条打真实相关度分，此时不得剥除——由调用方用
// HasScoringOperator 判定后决定是否调用本方法。
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

// scoringOperators 是会产生真实相关度分（逐条不同）的算子集合；其余结构化算子在
// OpenSearch 侧是常量打分，_score 无排序语义。
var scoringOperators = map[KnOperationType]struct{}{
	KnOperationTypeKnn:   {}, // 向量近邻，按相似度打分
	KnOperationTypeMatch: {}, // 全文匹配，按相关度打分
}

// HasScoringOperator 判断本次查询是否使用了会产生相关度评分的算子（knn / match），
// 用于决定响应是否保留 _score（#236）。会同时检查 filters 语法糖与已展开的
// condition 树，故调用时机无关（展开前后都正确）。
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

// condHasScoringOperator 递归判断 condition 树里是否含打分算子。
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

// ExploreSubgraphReq 起点探索式子图查询，对应下游 SubGraphQueryBaseOnSource
// （`POST /subgraph` 且 query_type 留空）。
//
// 与 QueryInstanceSubgraphReq 的路径模板模式互补，互不替代：那边要求调用方先把整条
// 路径的对象类序列与关系类序列拼出来，适合「拓扑已知、批量取数」；这边只要起点对象类
// + 方向 + 跳数，适合「拓扑未知、要发现关联」——后者才是 Agent 的默认提问形态。
type ExploreSubgraphReq struct {
	// 以下字段走 URL，不进请求体：整个结构体会被直接序列化成 body 发给下游。
	KnID               string `json:"-" form:"kn_id"`
	IncludeLogicParams bool   `json:"-" form:"include_logic_params"`
	// ExcludeSystemProperties / IgnoringStoreCache 只供服务内部调用方使用，不进 MCP
	// 工具 schema，理由与 QueryObjectInstancesReq 上的同名字段一致。
	//
	// 关于 ExcludeSystemProperties 在本接口是否生效：下游确实把嵌套的起点对象查询
	// （startObjectQuery）里这行赋值注释掉了，但那**不代表参数无效**——子图的系统字段
	// 由子图层自己生成而非起点查询带出（那行注释旁边写着这个原因），裁剪发生在
	// expandObjectPathsBatch 组装 ObjectInfoInSubgraph 时，读的正是
	// query.ExcludeSystemProperties。所以照常透传。
	ExcludeSystemProperties []string `json:"-" form:"exclude_system_properties"`
	IgnoringStoreCache      bool     `json:"-" form:"ignoring_store_cache"`

	// SourceObjectTypeID 探索起点的对象类。
	SourceObjectTypeID string `json:"source_object_type_id"`
	// Direction 探索方向，取 forward / backward / bidirectional，由下游校验。
	Direction string `json:"direction"`
	// PathLength 最大跳数，取 1-3。
	//
	// 上界与下游 validateSubgraphSearchRequest 一致（超 3 回 400）；**下界必须钉在这里**：
	// 0 是 int 零值，与「没传」不可区分，而下游对 0 不报错、只返回空子图——调用方会把
	// 「参数漏填」读成「什么都没连上」。校验挂在结构体上而不是各入口里，REST 与 MCP
	// 才共用同一条规则（MCP 侧另有一次显式检查，为的是把字段名点出来）。
	PathLength int `json:"path_length" validate:"min=1,max=3"`
	// ConceptGroups 按概念分组圈定探索范围，不传则不限。
	ConceptGroups []string `json:"concept_groups,omitempty"`
	// Cond 起点对象类的过滤条件，与 query_object_instance 的 condition 同构。
	Cond *KnCondition `json:"condition,omitempty"`
	// IncludeIncompletePath 是否返回「已走过至少一条边但类型路径没走完」的残缺路径，
	// 默认 false。零边路径任何情况下都不返回。
	IncludeIncompletePath bool `json:"include_incomplete_path,omitempty"`

	// 以下四项作用于**起点对象类**，不是整张子图：下游 SubGraphQueryBaseOnSource
	// 内嵌 PageQuery，分页与排序都只切起点集合。
	Sort        []*SortSpec `json:"sort,omitempty"`
	Limit       int         `json:"limit" validate:"min=1,max=10000" default:"10"`
	Offset      int         `json:"offset,omitempty"`
	SearchAfter []any       `json:"search_after,omitempty"`
	// NeedTotal 由 driven adapter 无条件置 true，不对外开放，理由同对象查询。
	NeedTotal bool `json:"need_total"`
}

// ExploreSubgraphResp 对应下游 ObjectSubGraph。
//
// 注意它与 QueryInstanceSubgraphResp 的关系：下游 PathsEntries 就是
// `{ entries: []ObjectSubGraph }`，即路径模板模式返回的是本结构的**数组**，探索模式
// 返回单个。两者元素同型，差别只在「一个」还是「一组」。
type ExploreSubgraphResp struct {
	// Objects 参与关系的对象，key 是对象 id。
	Objects map[string]any `json:"objects"`
	// IsolatedObjects 未与其余对象建立关系的孤立对象。**这是有效结论不是空结果**：
	// 它明确回答了「这些起点之间/向外没有关联」。
	IsolatedObjects map[string]any `json:"isolated_objects,omitempty"`
	// RelationPaths 具体的关系路径集合。
	RelationPaths []any `json:"relation_paths"`
	// TotalCount 起点对象类的命中总数，三态语义与 QueryObjectInstancesResp.TotalCount
	// 完全一致（有值 >0 / 有值 =0 / 缺失表示没算），见 resolveAbsentTotal。
	TotalCount *int64 `json:"total_count,omitempty"`
	// SearchAfter 起点对象类的下一页游标。
	SearchAfter []any `json:"search_after,omitempty"`
	// CurrentPathNumber 下游回填的当前路径序号。
	CurrentPathNumber int `json:"current_path_number,omitempty"`
}

// DrivenOntologyQuery Ontology query interface
type DrivenOntologyQuery interface {
	// QueryObjectInstances retrieves detailed data of objects for a specified object class
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
