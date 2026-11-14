// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

const (
	BuildScheduleSortName       = "name"
	BuildScheduleSortCreateTime = "create_time"
	BuildScheduleSortUpdateTime = "update_time"
	BuildScheduleSortNextRun    = "next_run"
)

var BUILD_SCHEDULE_SORT = map[string]struct{}{
	BuildScheduleSortName:       {},
	BuildScheduleSortCreateTime: {},
	BuildScheduleSortUpdateTime: {},
	BuildScheduleSortNextRun:    {},
}

// BuildSchedule 保存周期构建配置，执行记录由 BuildTask 承载。
type BuildSchedule struct {
	ID           string      `json:"id"`
	Name         string      `json:"name"`
	ResourceID   string      `json:"resource_id"`
	CatalogID    string      `json:"catalog_id"`
	ResourceName string      `json:"resource_name,omitempty"`
	CatalogName  string      `json:"catalog_name,omitempty"`
	CronExpr     string      `json:"cron_expr"`
	ExecuteType  string      `json:"execute_type"`
	StartTime    int64       `json:"start_time"`
	EndTime      int64       `json:"end_time"`
	Enabled      bool        `json:"enabled"`
	LastRun      int64       `json:"last_run"`
	NextRun      int64       `json:"next_run"`
	LastStatus   string      `json:"last_status"`
	LastMessage  string      `json:"last_message"`
	RunID        string      `json:"-"`
	Creator      AccountInfo `json:"creator"`
	CreateTime   int64       `json:"create_time"`
	Updater      AccountInfo `json:"updater"`
	UpdateTime   int64       `json:"update_time"`
}

// BuildScheduleRequest 只允许更新业务配置，不接受最近触发结果。
type BuildScheduleRequest struct {
	Name               string `json:"name"`
	ResourceID         string `json:"resource_id"`
	CronExpr           string `json:"cron_expr"`
	ExecuteType        string `json:"execute_type"`
	StartTime          int64  `json:"start_time"`
	EndTime            int64  `json:"end_time"`
	Enabled            bool   `json:"enabled"`
	ExpectedUpdateTime int64  `json:"expected_update_time,omitempty"`
}

type BuildScheduleQueryParams struct {
	PaginationQueryParams
	Name              string
	ResourceID        string
	CatalogID         string
	ExecuteType       string
	Enabled           *bool
	CatalogIDs        []string
	ExcludeCatalogIDs []string
}
