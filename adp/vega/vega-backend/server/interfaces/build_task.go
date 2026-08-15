// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "strings"

const (
	BuildTaskStatusPending   string = "pending"
	BuildTaskStatusRunning   string = "running"
	BuildTaskStatusCompleted string = "completed"
	BuildTaskStatusStopping  string = "stopping"
	BuildTaskStatusStopped   string = "stopped"
	BuildTaskStatusFailed    string = "failed"
	BuildTaskStatusCancelled string = "cancelled"

	BuildTaskModeStreaming string = "streaming" // 流式
	BuildTaskModeBatch     string = "batch"     // 批量

	BuildTaskSortCreateTime       string = "create_time"
	BuildTaskSortStartTime        string = "start_time"
	BuildTaskSortFinishTime       string = "finish_time"
	BuildTaskSortLastProgressTime string = "last_progress_time"

	BuildTaskExecuteTypeIncremental string = "incremental" // 增量
	BuildTaskExecuteTypeFull        string = "full"        // 全量

	EmptyDocumentID string = "empty_document"

	BUILD_TASK_RETRY_INTERVAL = 5 // 重试间隔，单位秒

	BUILD_PREFIX = "vega-build"
)

// BUILD_TASK_SORT is a whitelist of supported sort values. Values are unused;
// the data access layer owns the mapping from API fields to database columns.
var BUILD_TASK_SORT = map[string]string{
	BuildTaskSortCreateTime:       "",
	BuildTaskSortStartTime:        "",
	BuildTaskSortFinishTime:       "",
	BuildTaskSortLastProgressTime: "",
}

var ConnectorClassMapping = map[string]string{
	ConnectorTypeMySQL:      "io.debezium.connector.mysql.MySqlConnector",
	ConnectorTypePostgreSQL: "io.debezium.connector.postgresql.PostgresConnector",
}

// BuildTask represents a build task entity.
type BuildTask struct {
	ID               string                `json:"id"`
	ResourceID       string                `json:"resource_id"`
	Status           string                `json:"status"`
	Mode             string                `json:"mode"`                   // 任务模式：streaming/batch
	ExecuteType      string                `json:"execute_type,omitempty"` // batch 执行类型：incremental/full；streaming 不适用
	TotalCount       int64                 `json:"total_count"`            // 总数
	SyncedCount      int64                 `json:"synced_count"`           // 已同步数
	VectorizedCount  int64                 `json:"vectorized_count"`       // 已做向量数
	SyncedMark       string                `json:"synced_mark"`            // 同步标记
	ErrorMsg         string                `json:"error_msg,omitempty"`
	FailureDetail    string                `json:"failure_detail,omitempty"` // 构建完成但部分文档向量化失败的明细，区别于 error_msg 的整任务硬失败
	Creator          AccountInfo           `json:"creator"`
	CreateTime       int64                 `json:"create_time"`
	StartTime        int64                 `json:"start_time,omitempty"`
	FinishTime       int64                 `json:"finish_time,omitempty"`
	LastProgressTime int64                 `json:"last_progress_time,omitempty"`
	IndexConfig      *BuildTaskIndexConfig `json:"index_config,omitempty"` // 创建 task 时从 resource 派生的索引配置快照
	CatalogID        string                `json:"catalog_id"`
	// 以下关联字段仅用于响应展示，不落库；由 service 按当前任务集合批量补齐。
	ResourceName string `json:"resource_name,omitempty"`
	CatalogName  string `json:"catalog_name,omitempty"`

	// IndexHealth 为响应时计算的派生状态，**不落库**：让消费方无需自己推断
	// "completed 其实是失败"。service 层在返回前填充。
	IndexHealth *IndexHealth `json:"index_health,omitempty"`
}

// BuildTaskSummary is the lightweight representation returned by list APIs.
// Index configuration snapshots and detailed partial-failure diagnostics are
// available from GetByID.
type BuildTaskSummary struct {
	ID               string       `json:"id"`
	ResourceID       string       `json:"resource_id"`
	ResourceName     string       `json:"resource_name,omitempty"`
	CatalogID        string       `json:"catalog_id"`
	CatalogName      string       `json:"catalog_name,omitempty"`
	Status           string       `json:"status"`
	Mode             string       `json:"mode"`
	ExecuteType      string       `json:"execute_type,omitempty"`
	TotalCount       int64        `json:"total_count"`
	SyncedCount      int64        `json:"synced_count"`
	VectorizedCount  int64        `json:"vectorized_count"`
	SyncedMark       string       `json:"synced_mark"`
	ErrorMsg         string       `json:"error_msg,omitempty"`
	Creator          AccountInfo  `json:"creator"`
	CreateTime       int64        `json:"create_time"`
	StartTime        int64        `json:"start_time,omitempty"`
	FinishTime       int64        `json:"finish_time,omitempty"`
	LastProgressTime int64        `json:"last_progress_time,omitempty"`
	IndexHealth      *IndexHealth `json:"index_health,omitempty"`

	// IndexConfig is loaded only to derive IndexHealth and is never serialized.
	IndexConfig *BuildTaskIndexConfig `json:"-"`
}

// BuildTaskProgress describes persisted execution progress without changing task status.
type BuildTaskProgress struct {
	TotalCount      *int64
	SyncedCount     *int64
	VectorizedCount *int64
	SyncedMark      *string
	FailureDetail   *string
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
	Statuses   []string // 多值状态过滤(IN);空为不过滤
	Mode       string
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

// BuildTaskIDFromIndexName 从本地索引名反解出产出它的构建任务 id。
//
// 索引由哪个构建任务产出，决定了里面的字段是按哪份配置建的——查询侧要用建索引时
// 的 embedding 模型，就得找回那份快照，而不是读资源上「现在」写着什么。资源上的
// 配置改了但没重建索引时，这两者会不一致。
// 不是本服务生成的索引名返回空串。
func BuildTaskIDFromIndexName(indexName string) string {
	prefix := BUILD_PREFIX + "-"
	if !strings.HasPrefix(indexName, prefix) {
		return ""
	}
	// 形如 vega-build-<resourceID>-<buildTaskID>
	parts := strings.Split(strings.TrimPrefix(indexName, prefix), "-")
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-1]
}
