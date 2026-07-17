// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

const (
	SemanticUnderstandingTaskScopeResource string = "resource"
	SemanticUnderstandingTaskScopeCatalog  string = "catalog"

	SemanticUnderstandingTaskStatusPending   string = "pending"
	SemanticUnderstandingTaskStatusRunning   string = "running"
	SemanticUnderstandingTaskStatusSucceeded string = "succeeded"
	SemanticUnderstandingTaskStatusFailed    string = "failed"

	SemanticUnderstandingApplyModeDryRun    string = "dry_run"
	SemanticUnderstandingApplyModeFillEmpty string = "fill_empty"
	SemanticUnderstandingApplyModeForce     string = "force"

	SemanticUnderstandingResourceAgentID string = "resource-semantic-understanding"
	SemanticUnderstandingCatalogAgentID  string = "catalog-semantic-understanding"

	SemanticUnderstandingTaskType string = "semantic-understanding:execute"

	DefaultSemanticUnderstandingLanguage            string  = "zh-CN"
	DefaultSemanticUnderstandingConfidenceThreshold float64 = 0.75
)

var (
	SemanticUnderstandingTaskActiveStatuses = []string{
		SemanticUnderstandingTaskStatusPending,
		SemanticUnderstandingTaskStatusRunning,
	}

	SEMANTIC_UNDERSTANDING_TASK_SORT = map[string]string{
		"create_time": "create_time",
		"update_time": "update_time",
		"status":      "status",
		"scope":       "scope",
	}
)

// SemanticUnderstandingTask records one Vega semantic-understanding async task
// and the external bkn-agent task/output associated with it.
type SemanticUnderstandingTask struct {
	ID                   string      `json:"id"`
	Scope                string      `json:"scope"`
	CatalogID            string      `json:"catalog_id"`
	ResourceID           string      `json:"resource_id,omitempty"`
	AgentTaskID          string      `json:"agent_task_id,omitempty"`
	AgentID              string      `json:"agent_id"`
	Input                string      `json:"input"`
	InputHash            string      `json:"input_hash"`
	Status               string      `json:"status"`
	ApplyMode            string      `json:"apply_mode"`
	ResultJSON           string      `json:"result_json,omitempty"`
	ConfidenceThreshold  float64     `json:"confidence_threshold"`
	Confidence           float64     `json:"confidence"`
	ConfidenceDetailJSON string      `json:"confidence_detail_json,omitempty"`
	ApplyDetailJSON      string      `json:"apply_detail_json,omitempty"`
	Applied              bool        `json:"applied"`
	AppliedTime          int64       `json:"applied_time,omitempty"`
	FailureDetail        string      `json:"failure_detail,omitempty"`
	Creator              AccountInfo `json:"creator"`
	CreateTime           int64       `json:"create_time"`
	UpdateTime           int64       `json:"update_time"`
}

type SemanticUnderstandingSamplePolicy struct {
	Masked  bool `json:"masked"`
	MaxRows int  `json:"max_rows"`
}

// CreateSemanticUnderstandingTaskRequest is the HTTP request body for creating a
// semantic-understanding task. Target resource/catalog is referenced by ID;
// input snapshots are still built internally by Vega.
type CreateSemanticUnderstandingTaskRequest struct {
	Scope      string `json:"scope"`
	CatalogID  string `json:"catalog_id,omitempty"`
	ResourceID string `json:"resource_id,omitempty"`

	ApplyMode           string                             `json:"apply_mode,omitempty"`
	ConfidenceThreshold *float64                           `json:"confidence_threshold,omitempty"`
	IncludeSampleRows   bool                               `json:"include_sample_rows,omitempty"`
	SamplePolicy        *SemanticUnderstandingSamplePolicy `json:"sample_policy,omitempty"`
}

type SemanticUnderstandingTaskQueryParams struct {
	PaginationQueryParams
	Scope      string
	CatalogID  string
	ResourceID string
	Statuses   []string
}

type SemanticUnderstandingTaskMessage struct {
	TaskID string `json:"task_id"`
}

type SemanticUnderstandingApplyResult struct {
	Applied    bool
	DetailJSON string
}

type SemanticUnderstandingSkippedApplyDetail struct {
	Reason              string  `json:"reason"`
	Confidence          float64 `json:"confidence,omitempty"`
	ConfidenceThreshold float64 `json:"confidence_threshold,omitempty"`
	ApplyMode           string  `json:"apply_mode,omitempty"`
	Scope               string  `json:"scope,omitempty"`
}
