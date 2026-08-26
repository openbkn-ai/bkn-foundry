// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

const (
	CatalogTypePhysical string = "physical"
	CatalogTypeLogical  string = "logical"

	CatalogSortName       string = "name"
	CatalogSortCreateTime string = "create_time"
	CatalogSortUpdateTime string = "update_time"

	CatalogHealthStatusHealthy   string = "healthy"
	CatalogHealthStatusDegraded  string = "degraded"
	CatalogHealthStatusUnhealthy string = "unhealthy"
	CatalogHealthStatusOffline   string = "offline"
	CatalogHealthStatusUnchecked string = "unchecked"

	CatalogDeletionBlockerProtectedResources                = "protected_resources"
	CatalogDeletionBlockerBuildTasksRunningOrStopping       = "build_tasks_running_or_stopping"
	CatalogDeletionBlockerDiscoverTasksRunning              = "discover_tasks_running"
	CatalogDeletionBlockerSemanticUnderstandingTasksRunning = "semantic_understanding_tasks_running"
)

// CATALOG_SORT is a whitelist of supported API sort fields. The data access
// layer maps these fields to database columns.
var CATALOG_SORT = map[string]string{
	CatalogSortName:       "",
	CatalogSortCreateTime: "",
	CatalogSortUpdateTime: "",
}

type CatalogDeletionTaskImpact struct {
	WillCancel int64 `json:"will_cancel"`
	Blocking   int64 `json:"blocking"`
}

// CatalogDeletionImpact describes the current deletion impact for one catalog.
// CanDelete mirrors the guards enforced by DELETE /catalogs/{id}.
type CatalogDeletionImpact struct {
	CatalogID                   string                    `json:"catalog_id"`
	CanDelete                   bool                      `json:"can_delete"`
	Blockers                    []string                  `json:"blockers"`
	BuildTasks                  CatalogDeletionTaskImpact `json:"build_tasks"`
	DiscoverTasks               CatalogDeletionTaskImpact `json:"discover_tasks"`
	SemanticUnderstandingTasks  CatalogDeletionTaskImpact `json:"semantic_understanding_tasks"`
	DiscoverSchedules           int64                     `json:"discover_schedules"`
	CatalogHealthCheckSchedules int64                     `json:"catalog_health_check_schedules"`
	Resources                   int                       `json:"resources"`
	ProtectedResources          int                       `json:"protected_resources"`
	ResourceIDs                 []string                  `json:"-"`
}

type CatalogHealthCheckStatus struct {
	HealthCheckStatus string `json:"health_check_status"`
	LastCheckTime     int64  `json:"last_check_time"`
	HealthCheckResult string `json:"health_check_result"`
}

// Catalog represents a Catalog entity.
type Catalog struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Tags        []string `json:"tags"`
	Description string   `json:"description"`

	Type    string `json:"type"`
	Enabled bool   `json:"enabled"`
	// Internal system internal directory: Register in the permission service as internal_catalog type, visible only to super administrators
	Internal bool `json:"internal"`

	ConnectorType string          `json:"connector_type"`
	ConnectorCfg  ConnectorConfig `json:"connector_config"`
	Metadata      map[string]any  `json:"metadata"`

	CatalogHealthCheckStatus

	Creator    AccountInfo `json:"creator"`
	CreateTime int64       `json:"create_time"`
	Updater    AccountInfo `json:"updater"`
	UpdateTime int64       `json:"update_time"`

	Operations []string `json:"operations"`
}

// CatalogSummary contains the fields returned by catalog list operations.
// Connection configuration and metadata are loaded only for catalog details.
type CatalogSummary struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Tags        []string `json:"tags"`
	Description string   `json:"description"`

	Type     string `json:"type"`
	Enabled  bool   `json:"enabled"`
	Internal bool   `json:"internal"`

	ConnectorType string `json:"connector_type"`

	CatalogHealthCheckStatus

	Creator    AccountInfo `json:"creator"`
	CreateTime int64       `json:"create_time"`
	Updater    AccountInfo `json:"updater"`
	UpdateTime int64       `json:"update_time"`

	Operations []string `json:"operations"`
}

// CatalogsQueryParams holds catalog list query parameters.
type CatalogsQueryParams struct {
	PaginationQueryParams
	Name              string
	Tag               string
	Type              string
	ConnectorType     string
	Enabled           *bool
	HealthCheckStatus string
}

// CatalogCreateRequest represents create catalog request.
type CatalogRequest struct {
	ID            string          `json:"id,omitempty"`
	Name          string          `json:"name"`
	Tags          []string        `json:"tags"`
	Description   string          `json:"description"`
	Enabled       bool            `json:"enabled"`
	ConnectorType string          `json:"connector_type"`
	ConnectorCfg  ConnectorConfig `json:"connector_config"`

	// Internal only takes effect during creation and cannot be changed by updates.
	// The create permission of type internal_catalog is required (by default, only for the super administrator/system S2S identity).
	Internal bool `json:"internal,omitempty"`

	// HealthCheckSchedule only takes effect when the physical directory is created; Create the default Schedule of the inherit mode when nil.
	HealthCheckSchedule *CatalogHealthCheckScheduleRequest `json:"health_check_schedule,omitempty"`

	ExpectedUpdateTime int64 `json:"expected_update_time,omitempty"`
}

// CatalogConnectionTestRequest represents an unpersisted physical catalog connection test.
type CatalogConnectionTestRequest struct {
	ConnectorType string          `json:"connector_type"`
	ConnectorCfg  ConnectorConfig `json:"connector_config"`
}
