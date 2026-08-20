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

	BuildTaskModeStreaming string = "streaming" // Flow cytometry
	BuildTaskModeBatch     string = "batch"     // Batch production

	BuildTaskSortCreateTime       string = "create_time"
	BuildTaskSortStartTime        string = "start_time"
	BuildTaskSortFinishTime       string = "finish_time"
	BuildTaskSortLastProgressTime string = "last_progress_time"

	BuildTaskExecuteTypeIncremental string = "incremental" // Increment
	BuildTaskExecuteTypeFull        string = "full"        // Full quantity

	EmptyDocumentID string = "empty_document"

	BUILD_TASK_RETRY_INTERVAL = 5 // Retry interval, unit: seconds

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
	Mode             string                `json:"mode"`                   // Task mode: streaming/batch
	ExecuteType      string                `json:"execute_type,omitempty"` // Batch execution type: incremental/full; not applicable to streaming.
	TotalCount       int64                 `json:"total_count"`            // Total number of documents
	SyncedCount      int64                 `json:"synced_count"`           // Number of synchronized documents
	SyncedMark       string                `json:"synced_mark"`            // Synchronization cursor
	ErrorMsg         string                `json:"error_msg,omitempty"`
	FailureDetail    string                `json:"failure_detail,omitempty"` // The details of the completed construction but partial document vectorization failure, which is different from the hard failure of the entire task of error_msg
	Creator          AccountInfo           `json:"creator"`
	CreateTime       int64                 `json:"create_time"`
	StartTime        int64                 `json:"start_time,omitempty"`
	FinishTime       int64                 `json:"finish_time,omitempty"`
	LastProgressTime int64                 `json:"last_progress_time,omitempty"`
	IndexConfig      *BuildTaskIndexConfig `json:"index_config,omitempty"` // A snapshot of the index configuration derived from resource when creating a task
	CatalogID        string                `json:"catalog_id"`
	// The following associated fields are only used for response display and will not be included in the database. The service will batch complete the tasks according to the current task set.
	ResourceName string `json:"resource_name,omitempty"`
	CatalogName  string `json:"catalog_name,omitempty"`
}

// BuildTaskSummary is the lightweight representation returned by list APIs.
// Index configuration snapshots and detailed partial-failure diagnostics are
// available from GetByID.
type BuildTaskSummary struct {
	ID               string      `json:"id"`
	ResourceID       string      `json:"resource_id"`
	ResourceName     string      `json:"resource_name,omitempty"`
	CatalogID        string      `json:"catalog_id"`
	CatalogName      string      `json:"catalog_name,omitempty"`
	Status           string      `json:"status"`
	Mode             string      `json:"mode"`
	ExecuteType      string      `json:"execute_type,omitempty"`
	TotalCount       int64       `json:"total_count"`
	SyncedCount      int64       `json:"synced_count"`
	SyncedMark       string      `json:"synced_mark"`
	ErrorMsg         string      `json:"error_msg,omitempty"`
	Creator          AccountInfo `json:"creator"`
	CreateTime       int64       `json:"create_time"`
	StartTime        int64       `json:"start_time,omitempty"`
	FinishTime       int64       `json:"finish_time,omitempty"`
	LastProgressTime int64       `json:"last_progress_time,omitempty"`
}

// BuildTaskProgress describes persisted execution progress without changing task status.
type BuildTaskProgress struct {
	TotalCount    *int64
	SyncedCount   *int64
	SyncedMark    *string
	FailureDetail *string
}

type BuildTaskIndexConfig struct {
	BuildKeyFields []string                              `json:"build_key_fields,omitempty"`
	Features       map[string]BuildTaskFieldIndexFeature `json:"features,omitempty"`
}

type BuildTaskFieldIndexFeature struct {
	Vector   *SmallModel              `json:"vector,omitempty"`
	Fulltext *BuildTaskFulltextConfig `json:"fulltext,omitempty"`
}

type BuildTaskFulltextConfig struct {
	Analyzer string `json:"analyzer,omitempty"`
}

// CreateBuildTaskRequest represents the request to create a build task.
type CreateBuildTaskRequest struct {
	ResourceID  string `json:"resource_id" binding:"required"`                                    // Associate Resource ID
	Mode        string `json:"mode" binding:"required,oneof=streaming batch"`                     // Task mode: streaming/batch
	ExecuteType string `json:"execute_type,omitempty" binding:"omitempty,oneof=incremental full"` // Execution type, batch only; default full
}

// StartBuildTaskRequest represents the optional body for POST /build-tasks/{id}/start.
type StartBuildTaskRequest struct {
	Reset bool `json:"reset,omitempty"` // true ignores synced_mark and restarts from the beginning
}

// BuildTasksQueryParams holds filter + pagination parameters for listing build tasks.
type BuildTasksQueryParams struct {
	PaginationQueryParams
	ResourceID string
	// ResourceIDs narrows the listing to a set of resources, and ExcludeResourceIDs
	// removes one. Together they carry the authorization filter into the SQL, so
	// the count and the page agree. Only sets small enough to be worth a round
	// trip travel this way — see visibilityPushdownLimit.
	ResourceIDs        []string
	ExcludeResourceIDs []string
	CatalogID          string
	Statuses           []string // Multi-valued state filtering (IN) Empty means no filtering
	Mode               string
}

type KeyValue struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

// BuildIndexName returns the OpenSearch index name corresponding to the build task. Index name = Prefix - Resource ID- Task ID
// Correspond one-to-one with the tasks. When deleting tasks/resources/directories, drop the index accordingly to avoid orphan indexes.
// Single source: Use worker to build indexes and clean up cascades in various departments. Don't manually assemble in multiple places.
func BuildIndexName(resourceID, buildTaskID string) string {
	return BUILD_PREFIX + "-" + resourceID + "-" + buildTaskID
}

// The build task id that produces "BuildTaskIDFromIndexName" is inverted from the local index name.
//
// Which build task produces the index determines which configuration the fields inside are created according to - when the query side needs to create an index
// For the embedding model, it is necessary to retrieve that snapshot instead of reading what is written "now" on the resource. Resource-based
// When the configuration is changed but the index is not rebuilt, the two will be inconsistent.
// An empty string is returned if the index name is not generated by this service.
func BuildTaskIDFromIndexName(indexName string) string {
	prefix := BUILD_PREFIX + "-"
	if !strings.HasPrefix(indexName, prefix) {
		return ""
	}
	// In the form of vega-build-<resourceID>-<buildTaskID>
	parts := strings.Split(strings.TrimPrefix(indexName, prefix), "-")
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-1]
}
