// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package interfaces defines entities, DTOs, and service interfaces.
package interfaces

const (
	// DiscoverTask status constants.
	DiscoverTaskStatusPending   string = "pending"
	DiscoverTaskStatusRunning   string = "running"
	DiscoverTaskStatusCompleted string = "completed"
	DiscoverTaskStatusFailed    string = "failed"
	DiscoverTaskStatusCancelled string = "cancelled"

	// DiscoverTask trigger type constants.
	DiscoverTaskTriggerManual    string = "manual"    // 手动/立即执行
	DiscoverTaskTriggerScheduled string = "scheduled" // 定时驱动

	// DiscoverTaskType is the task type for discover tasks.
	DiscoverTaskType = "discover:execute"

	// KafkaTopic is the topic for discover task messages.
	DiscoverTaskTopic = "adp-vega-discover-task"
)

var (
	DISCOVER_TASK_SORT = map[string]string{
		"create_time": "",
		"start_time":  "",
		"finish_time": "",
	}
)

// DiscoverTask represents a discover task entity.
type DiscoverTask struct {
	ID          string `json:"id"`
	CatalogID   string `json:"catalog_id"`
	CatalogName string `json:"catalog_name,omitempty"`
	ScheduleID  string `json:"schedule_id"`
	Strategy    string `json:"strategy"`     // Discover strategy: full_sync/create_only/cleanup_only
	TriggerType string `json:"trigger_type"` // manual/scheduled

	Status     string          `json:"status"`   // pending/running/completed/failed/cancelled
	Progress   int             `json:"progress"` // 0-100
	Message    string          `json:"message"`
	StartTime  int64           `json:"start_time,omitempty"`  // 开始执行时间
	FinishTime int64           `json:"finish_time,omitempty"` // 完成时间
	Result     *DiscoverResult `json:"result,omitempty"`

	Creator    AccountInfo `json:"creator"`
	CreateTime int64       `json:"create_time"`

	// DiscoverActions is derived from Strategy by the worker and is not persisted.
	DiscoverActions *DiscoverActions `json:"-"`
}

// DiscoverTaskSummary is the lightweight representation returned by list APIs.
// The full task result and its execution message are available from GetByID.
type DiscoverTaskSummary struct {
	ID          string `json:"id"`
	CatalogID   string `json:"catalog_id"`
	CatalogName string `json:"catalog_name,omitempty"`
	ScheduleID  string `json:"schedule_id"`
	Strategy    string `json:"strategy"`
	TriggerType string `json:"trigger_type"`

	Status     string                     `json:"status"`
	Progress   int                        `json:"progress"`
	StartTime  int64                      `json:"start_time,omitempty"`
	FinishTime int64                      `json:"finish_time,omitempty"`
	Result     *DiscoverTaskResultSummary `json:"result,omitempty"`

	Creator    AccountInfo `json:"creator"`
	CreateTime int64       `json:"create_time"`
}

// DiscoverTaskResultSummary contains the result counters used by task lists.
// The detailed execution message is only returned from GetByID.
type DiscoverTaskResultSummary struct {
	CatalogID      string `json:"catalog_id"`
	NewCount       int    `json:"new_count"`
	StaleCount     int    `json:"stale_count"`
	UnchangedCount int    `json:"unchanged_count"`
	UpdatedCount   int    `json:"updated_count"`
	RestoredCount  int    `json:"restored_count"`
	FailedCount    int    `json:"failed_count"`
}

// DiscoverTaskQueryParams holds discover task list query parameters.
type DiscoverTaskQueryParams struct {
	PaginationQueryParams
	CatalogID   string   `form:"catalog_id" json:"catalog_id"`
	ScheduleID  string   `form:"schedule_id" json:"schedule_id"`
	Statuses    []string `form:"status" json:"status"`
	Strategy    string   `form:"strategy" json:"strategy"`
	TriggerType string   `form:"trigger_type" json:"trigger_type"`
}

// DiscoverTaskMessage represents the Kafka message for discover task.
type DiscoverTaskMessage struct {
	TaskID string `json:"task_id"`
}

type CreateDiscoverTaskRequest struct {
	CatalogID   string
	TriggerType string
	ScheduleID  string
	Strategy    string
}
