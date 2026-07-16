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

	BuildTaskExecuteTypeIncremental string = "incremental" // 增量
	BuildTaskExecuteTypeFull        string = "full"        // 全量

	EmptyDocumentID string = "empty_document"

	BUILD_TASK_RETRY_INTERVAL = 5 // 重试间隔，单位秒

	BUILD_PREFIX = "vega-build"
)

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
	ID              string                `json:"id"`
	ResourceID      string                `json:"resource_id"`
	Status          string                `json:"status"`
	Mode            string                `json:"mode"`             // 任务模式：streaming/batch
	TotalCount      int64                 `json:"total_count"`      // 总数
	SyncedCount     int64                 `json:"synced_count"`     // 已同步数
	VectorizedCount int64                 `json:"vectorized_count"` // 已做向量数
	SyncedMark      string                `json:"synced_mark"`      // 同步标记
	ErrorMsg        string                `json:"error_msg,omitempty"`
	FailureDetail   string                `json:"failure_detail,omitempty"` // 构建完成但部分文档向量化失败的明细，区别于 error_msg 的整任务硬失败
	Creator         AccountInfo           `json:"creator"`
	CreateTime      int64                 `json:"create_time"`
	UpdateTime      int64                 `json:"update_time"`
	IndexConfig     *BuildTaskIndexConfig `json:"index_config,omitempty"` // 创建 task 时从 resource 派生的索引配置快照
	CatalogID       string                `json:"catalog_id"`

	// IndexHealth 为响应时计算的派生状态，**不落库**：让消费方无需自己推断
	// "completed 其实是失败"。service 层在返回前填充。
	IndexHealth *IndexHealth `json:"index_health,omitempty"`
}

// BuildTaskUpdate describes a partial build task update. Nil fields are left unchanged.
type BuildTaskUpdate struct {
	Status          *string
	TotalCount      *int64
	SyncedCount     *int64
	VectorizedCount *int64
	SyncedMark      *string
	ErrorMsg        *string
	FailureDetail   *string
}

func NewBuildTaskUpdate() BuildTaskUpdate {
	return BuildTaskUpdate{}
}

func (u BuildTaskUpdate) WithStatus(status string) BuildTaskUpdate {
	u.Status = &status
	return u
}

func (u BuildTaskUpdate) WithTotalCount(totalCount int64) BuildTaskUpdate {
	u.TotalCount = &totalCount
	return u
}

func (u BuildTaskUpdate) WithSyncedCount(syncedCount int64) BuildTaskUpdate {
	u.SyncedCount = &syncedCount
	return u
}

func (u BuildTaskUpdate) WithVectorizedCount(vectorizedCount int64) BuildTaskUpdate {
	u.VectorizedCount = &vectorizedCount
	return u
}

func (u BuildTaskUpdate) WithSyncedMark(syncedMark string) BuildTaskUpdate {
	u.SyncedMark = &syncedMark
	return u
}

func (u BuildTaskUpdate) WithErrorMsg(errorMsg string) BuildTaskUpdate {
	u.ErrorMsg = &errorMsg
	return u
}

func (u BuildTaskUpdate) WithFailureDetail(failureDetail string) BuildTaskUpdate {
	u.FailureDetail = &failureDetail
	return u
}

type BuildTaskIndexConfig struct {
	BuildKeyFields []string                              `json:"build_key_fields,omitempty"`
	Features       map[string]BuildTaskFieldIndexFeature `json:"features,omitempty"`
}

type BuildTaskFieldIndexFeature struct {
	Vector   *BuildTaskEmbeddingConfig `json:"vector,omitempty"`
	Fulltext *BuildTaskFulltextConfig  `json:"fulltext,omitempty"`
}

type BuildTaskEmbeddingConfig struct {
	ModelID    string `json:"model_id"`
	Dimensions int    `json:"dimensions"`
}

type BuildTaskFulltextConfig struct {
	Analyzer string `json:"analyzer,omitempty"`
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
type CreateBuildTaskRequest struct {
	ResourceID  string `json:"resource_id" binding:"required"`                                    // 关联 Resource ID
	Mode        string `json:"mode" binding:"required,oneof=streaming batch"`                     // 任务模式：streaming/batch
	ExecuteType string `json:"execute_type,omitempty" binding:"omitempty,oneof=incremental full"` // 执行类型, batch only; default full
}

// StartBuildTaskRequest represents the optional body for POST /build-tasks/{id}/start.
type StartBuildTaskRequest struct {
	Reset bool `json:"reset,omitempty"` // true ignores synced_mark and restarts from the beginning
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
