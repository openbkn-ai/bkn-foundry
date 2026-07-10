// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

const (
	BuildTaskStatusInit      string = "init"
	BuildTaskStatusRunning   string = "running"
	BuildTaskStatusCompleted string = "completed"
	BuildTaskStatusStopping  string = "stopping"
	BuildTaskStatusStopped   string = "stopped"
	BuildTaskStatusFailed    string = "failed"

	BuildTaskTypeBatch     string = "batch:execute"
	BuildTaskTypeStreaming string = "streaming:execute"
	BuildTaskTypeEmbedding string = "embedding:execute"

	BuildTaskModeStreaming string = "streaming" // 流式
	BuildTaskModeBatch     string = "batch"     // 批量

	// build-task 列表排序维度(query: order_by)
	BuildTaskOrderByDefault   string = "default"    // 活跃置顶分桶序(缺省)
	BuildTaskOrderByCreatedAt string = "created_at" // 按创建时间
	BuildTaskOrderByUpdatedAt string = "updated_at" // 按更新时间
	BuildTaskOrderByStatus    string = "status"     // 按状态优先级桶序
	BuildTaskOrderByMode      string = "mode"       // 按模式

	DEFAULT_BUILD_TASK_ORDER_BY string = BuildTaskOrderByDefault
	DEFAULT_BUILD_TASK_ORDER    string = DESC_DIRECTION

	BuildTaskExecuteTypeIncremental string = "incremental" // 增量
	BuildTaskExecuteTypeFull        string = "full"        // 全量

	EmptyDocumentID string = "empty_document"

	BUILD_TASK_MAX_RETRY_COUNT = 50 // 最大重试次数
	BUILD_TASK_RETRY_INTERVAL  = 5  // 重试间隔，单位秒

	BUILD_PREFIX = "vega-build"
)

func BuildTaskQueueTaskID(taskType string, taskID string) string {
	return taskType + ":" + taskID
}

// BuildTaskStatusOrder 定义 default/status 排序的状态桶优先级(下标+1 即优先级)。
// 活跃任务(running/init)置顶是核心诉求;顺序写死,既是 SQL CASE 的唯一来源,
// 也保证「构建中」永远排在第一页。
var BuildTaskStatusOrder = []string{
	BuildTaskStatusRunning,   // 1 构建中/流式监听中
	BuildTaskStatusInit,      // 2 排队中
	BuildTaskStatusStopping,  // 3 停止中
	BuildTaskStatusStopped,   // 4 已停止
	BuildTaskStatusFailed,    // 5 失败
	BuildTaskStatusCompleted, // 6 已完成
}

// BuildTask represents a build task entity.
type BuildTask struct {
	ID               string      `json:"id"`
	ResourceID       string      `json:"resource_id"`
	Status           string      `json:"status"`
	Mode             string      `json:"mode"`             // 任务模式：streaming/batch
	TotalCount       int64       `json:"total_count"`      // 总数
	SyncedCount      int64       `json:"synced_count"`     // 已同步数
	VectorizedCount  int64       `json:"vectorized_count"` // 已做向量数
	SyncedMark       string      `json:"synced_mark"`      // 同步标记
	ErrorMsg         string      `json:"error_msg,omitempty"`
	FailureDetail    string      `json:"failure_detail,omitempty"` // 构建完成但部分文档向量化失败的明细，区别于 error_msg 的整任务硬失败
	Creator          AccountInfo `json:"creator"`
	CreateTime       int64       `json:"create_time"`
	Updater          AccountInfo `json:"updater"`
	UpdateTime       int64       `json:"update_time"`
	EmbeddingFields  string      `json:"embedding_fields,omitempty"`  // 需向量化嵌入字段
	BuildKeyFields   string      `json:"build_key_fields"`            // 构建中依赖的特殊键字段，如批量构建依赖的有时序性的字段，流式构建依赖的唯一标识某行的字段
	EmbeddingModel   string      `json:"embedding_model,omitempty"`   // 嵌入模型
	ModelDimensions  int         `json:"model_dimensions,omitempty"`  // 模型维度
	FulltextFields   string      `json:"fulltext_fields,omitempty"`   // 需建全文索引的字段(逗号分隔)；string→加 text 子字段，text→主字段分词
	FulltextAnalyzer string      `json:"fulltext_analyzer,omitempty"` // 全文分词器(standard/ik_max_word/hanlp_index 等)，空为 OpenSearch 默认
	CatalogID        string      `json:"catalog_id"`

	// IndexHealth 为响应时计算的派生状态，**不落库**：让消费方无需自己推断
	// "completed 其实是失败"。service 层在返回前填充。
	IndexHealth *IndexHealth `json:"index_health,omitempty"`
}

// IndexHealth 拆分各索引的健康度。status=completed 只代表 sync 完成 + fulltext 生效，
// 不代表 embedding 索引可用——本结构把两者分开，整体可用性看 Usable。
type IndexHealth struct {
	// none(未建) | building(进行中) | ok | partial(部分文档缺向量) | failed(全部缺向量)
	Embedding string `json:"embedding"`
	// none(未建) | ok（全文随同步即时生效，建了即 ok）
	Fulltext string `json:"fulltext"`
	// embedding 索引是否完全可用（none 或 ok 为 true；partial/failed/building 为 false）
	Usable bool `json:"usable"`
}

// CreateBuildTaskRequest represents the request to create a build task.
// Used as both the HTTP body for POST /build-tasks and the service input.
type CreateBuildTaskRequest struct {
	ResourceID       string `json:"resource_id" binding:"required"`                // 关联 Resource ID
	Mode             string `json:"mode" binding:"required,oneof=streaming batch"` // 任务模式：streaming/batch
	EmbeddingFields  string `json:"embedding_fields,omitempty"`                    // 需向量化嵌入字段
	BuildKeyFields   string `json:"build_key_fields"`                              // 构建中依赖的特殊键字段，如批量构建依赖的有时序性的字段，流式构建依赖的唯一标识某行的字段
	EmbeddingModel   string `json:"embedding_model,omitempty"`                     // 嵌入模型
	ModelDimensions  int    `json:"model_dimensions,omitempty"`                    // 模型维度
	FulltextFields   string `json:"fulltext_fields,omitempty"`                     // 需建全文索引的字段(逗号分隔)
	FulltextAnalyzer string `json:"fulltext_analyzer,omitempty"`                   // 全文分词器，空为 OpenSearch 默认 standard
}

// UpdateBuildTaskStatusRequest represents update build task status request.
type UpdateBuildTaskStatusRequest struct {
	Status      string `json:"status" binding:"required,oneof=running stopped"` // 修改任务状态，只允许 running 和 stopped
	ExecuteType string `json:"execute_type,omitempty"`                          // 执行类型,for batch mode, default is "incremental"
}

// StartBuildTaskRequest represents the optional body for POST /build-tasks/{id}/start.
type StartBuildTaskRequest struct {
	ExecuteType string `json:"execute_type,omitempty"` // incremental / full; default incremental
}

// BuildTasksQueryParams holds filter + pagination parameters for listing build tasks.
type BuildTasksQueryParams struct {
	PaginationQueryParams
	ResourceID string
	CatalogID  string
	Statuses   []string // 多值状态过滤(IN);空为不过滤。active=true 等价 [running,init]
	Mode       string
	OrderBy    string // default|created_at|status|mode；缺省 default
	Order      string // asc|desc；缺省 desc。order_by=default 时忽略(固定复合序)
}

type KeyValue struct {
	Key   string
	Value any
}

// BuildIndexName 返回构建任务对应的 OpenSearch 索引名。索引名 = 前缀-资源ID-任务ID，
// 与任务一一对应。删除任务/资源/目录时据此 drop 索引，避免孤儿索引。
// 单一来源：worker 建索引与各处级联清理都用它，别在多处手拼。
func BuildIndexName(resourceID, buildTaskID string) string {
	return BUILD_PREFIX + "-" + resourceID + "-" + buildTaskID
}
