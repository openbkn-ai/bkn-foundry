// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

const (
	CatalogHealthCheckScheduleModeInherit  = "inherit"
	CatalogHealthCheckScheduleModeEnabled  = "enabled"
	CatalogHealthCheckScheduleModeDisabled = "disabled"
)

// CatalogHealthCheckSchedule configures periodic health checks for one physical Catalog.
type CatalogHealthCheckSchedule struct {
	CatalogID string `json:"catalog_id"`
	Mode      string `json:"mode"`
	CronExpr  string `json:"cron_expr,omitempty"`
	LastRun   int64  `json:"last_run"`
	NextRun   int64  `json:"next_run"`

	Creator    AccountInfo `json:"creator"`
	CreateTime int64       `json:"create_time"`
	Updater    AccountInfo `json:"updater"`
	UpdateTime int64       `json:"update_time"`
}

// CatalogHealthCheckScheduleRequest is the writable Schedule configuration.
type CatalogHealthCheckScheduleRequest struct {
	Mode               string `json:"mode"`
	CronExpr           string `json:"cron_expr,omitempty"`
	ExpectedUpdateTime int64  `json:"expected_update_time,omitempty"`
}
