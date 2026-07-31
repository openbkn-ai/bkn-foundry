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
	MaxSemanticUnderstandingSampleRows              int     = 20
	MaxSemanticUnderstandingSampleValueRunes        int     = 128
	MaxSemanticUnderstandingSamplePayloadBytes      int     = 128 * 1024
)

var (
	SemanticUnderstandingTaskActiveStatuses = []string{
		SemanticUnderstandingTaskStatusPending,
		SemanticUnderstandingTaskStatusRunning,
	}

	SEMANTIC_UNDERSTANDING_TASK_SORT = map[string]string{
		"default":     "",
		"create_time": "",
		"update_time": "",
	}
)

// SemanticUnderstandingTask records one Vega semantic-understanding async task
// and the external bkn-agent task/output associated with it.
type SemanticUnderstandingTask struct {
	ID                   string      `json:"id"`
	Scope                string      `json:"scope"`
	CatalogID            string      `json:"catalog_id"`
	CatalogName          string      `json:"catalog_name,omitempty"`
	ResourceID           string      `json:"resource_id,omitempty"`
	ResourceName         string      `json:"resource_name,omitempty"`
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

// SemanticUnderstandingResourceAgentInput is the audited payload sent to the
// resource semantic-understanding agent. It is shared by task creation and
// worker-side result quality evaluation.
type SemanticUnderstandingResourceAgentInput struct {
	Resource   SemanticUnderstandingResourceAgentInputResource `json:"resource"`
	SampleRows []map[string]any                                `json:"sample_rows"`
	Options    SemanticUnderstandingResourceAgentInputOptions  `json:"options"`
}

type SemanticUnderstandingResourceAgentInputResource struct {
	ID                string                                            `json:"id"`
	Name              string                                            `json:"name"`
	Category          string                                            `json:"category"`
	Schema            string                                            `json:"schema,omitempty"`
	SourceIdentifier  string                                            `json:"source_identifier"`
	SourceDescription string                                            `json:"source_description,omitempty"`
	Description       string                                            `json:"description,omitempty"`
	SchemaDefinition  []SemanticUnderstandingResourceAgentInputProperty `json:"schema_definition"`
}

type SemanticUnderstandingResourceAgentInputProperty struct {
	Name                string `json:"name"`
	Type                string `json:"type"`
	OriginalName        string `json:"original_name,omitempty"`
	OriginalType        string `json:"original_type,omitempty"`
	OriginalDescription string `json:"original_description,omitempty"`
	DisplayName         string `json:"display_name,omitempty"`
	Description         string `json:"description,omitempty"`
}

type SemanticUnderstandingResourceAgentInputOptions struct {
	Language            string                             `json:"language"`
	ApplyMode           string                             `json:"apply_mode"`
	ConfidenceThreshold float64                            `json:"confidence_threshold"`
	IncludeSampleRows   bool                               `json:"include_sample_rows"`
	SamplePolicy        *SemanticUnderstandingSamplePolicy `json:"sample_policy,omitempty"`
}

// SemanticUnderstandingResourceQuality summarizes the effective semantic
// enhancements produced by a resource-level agent result.
type SemanticUnderstandingResourceQuality struct {
	ResourceEffective bool `json:"resource_effective"`
	FieldTotal        int  `json:"field_total"`
	FieldEffective    int  `json:"field_effective"`
}

// SemanticUnderstandingCatalogAgentInput is the audited payload sent to the
// catalog semantic-understanding agent.
type SemanticUnderstandingCatalogAgentInput struct {
	Catalog            SemanticUnderstandingCatalogAgentInputCatalog        `json:"catalog"`
	Resources          []SemanticUnderstandingCatalogAgentInputResource     `json:"resources"`
	ExistingLogicViews []SemanticUnderstandingCatalogAgentInputExistingView `json:"existing_logic_views"`
	Options            SemanticUnderstandingCatalogAgentInputOptions        `json:"options"`
}

type SemanticUnderstandingCatalogAgentInputCatalog struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type SemanticUnderstandingCatalogAgentInputResource struct {
	ID               string                                              `json:"id"`
	Name             string                                              `json:"name"`
	Description      string                                              `json:"description,omitempty"`
	Schema           string                                              `json:"schema,omitempty"`
	SourceIdentifier string                                              `json:"source_identifier"`
	Keys             *SemanticUnderstandingCatalogAgentInputResourceKeys `json:"keys,omitempty"`
	Fields           []SemanticUnderstandingCatalogAgentInputProperty    `json:"fields"`
}

type SemanticUnderstandingCatalogAgentInputResourceKeys struct {
	Primary []string   `json:"primary,omitempty"`
	Unique  [][]string `json:"unique,omitempty"`
}

type SemanticUnderstandingCatalogAgentInputProperty struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

type SemanticUnderstandingCatalogAgentInputExistingView struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	SourceIdentifier string `json:"source_identifier"`
	Description      string `json:"description,omitempty"`
	Status           string `json:"status"`
}

type SemanticUnderstandingCatalogAgentInputOptions struct {
	Language            string  `json:"language"`
	ApplyMode           string  `json:"apply_mode"`
	ConfidenceThreshold float64 `json:"confidence_threshold"`
}

type SemanticUnderstandingTaskQueryParams struct {
	PaginationQueryParams
	Scope      string
	CatalogID  string
	ResourceID string
	Statuses   []string
	ApplyMode  string
	Applied    *bool
}

type SemanticUnderstandingTaskMessage struct {
	TaskID string `json:"task_id"`
}

type SemanticUnderstandingApplyResult struct {
	Applied    bool
	DetailJSON string
}

type SemanticUnderstandingFieldApplyDetail struct {
	Name    string   `json:"name"`
	Status  string   `json:"status"`
	Updated []string `json:"updated,omitempty"`
	Reasons []string `json:"reasons,omitempty"`
}

type SemanticUnderstandingResourceApplyDetail struct {
	ResourceUpdated bool                                    `json:"resource_updated"`
	UpdatedResource []string                                `json:"updated_resource,omitempty"`
	UpdatedFields   []string                                `json:"updated_fields,omitempty"`
	SkippedFields   []string                                `json:"skipped_fields,omitempty"`
	FieldDetails    []SemanticUnderstandingFieldApplyDetail `json:"field_details,omitempty"`
}

type SemanticUnderstandingResourceResult struct {
	Resource SemanticUnderstandingResourceResultResource `json:"resource"`
	Fields   []SemanticUnderstandingResourceResultField  `json:"fields"`
}

type SemanticUnderstandingResourceResultResource struct {
	DisplayName string   `json:"display_name"`
	Description string   `json:"description"`
	Confidence  *float64 `json:"confidence,omitempty"`
}

type SemanticUnderstandingResourceResultField struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name"`
	Description string   `json:"description"`
	Confidence  *float64 `json:"confidence,omitempty"`
}

type SemanticUnderstandingCatalogResult struct {
	LogicViews         []SemanticUnderstandingCatalogLogicView    `json:"logic_views"`
	ObsoleteLogicViews []SemanticUnderstandingCatalogObsoleteView `json:"obsolete_logic_views"`
}

type SemanticUnderstandingCatalogLogicView struct {
	Action           string                 `json:"action"`
	TargetResourceID string                 `json:"target_resource_id"`
	Name             string                 `json:"name"`
	SourceIdentifier string                 `json:"source_identifier"`
	Description      string                 `json:"description"`
	SourceResources  []string               `json:"source_resources"`
	LogicDefinition  []*LogicDefinitionNode `json:"logic_definition"`
	Confidence       *float64               `json:"confidence,omitempty"`
}

type SemanticUnderstandingCatalogObsoleteView struct {
	TargetResourceID string   `json:"target_resource_id"`
	Reason           string   `json:"reason"`
	Confidence       *float64 `json:"confidence,omitempty"`
}

type SemanticUnderstandingCatalogApplyDetail struct {
	CreatedResourceIDs []string `json:"created_resource_ids,omitempty"`
	UpdatedResourceIDs []string `json:"updated_resource_ids,omitempty"`
	StaledResourceIDs  []string `json:"staled_resource_ids,omitempty"`
}

type SemanticUnderstandingSkippedApplyDetail struct {
	Reason              string  `json:"reason"`
	Confidence          float64 `json:"confidence,omitempty"`
	ConfidenceThreshold float64 `json:"confidence_threshold,omitempty"`
	ApplyMode           string  `json:"apply_mode,omitempty"`
	Scope               string  `json:"scope,omitempty"`
}
