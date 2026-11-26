// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "github.com/openbkn-ai/bkn-foundry/comm-go/audit"

// Schedule status constants
const (
	ScheduleStatusActive   = "active"
	ScheduleStatusInactive = "inactive"
)

// ActionScheduleSourceActionType marks a schedule the platform keeps for a record-event action
// type of the running version: its expression, name and execution subject follow the action type,
// and it cannot be changed through the schedule API. A user's schedule has no source.
const ActionScheduleSourceActionType = "action_type"

// ActionSchedule represents a scheduled action configuration
type ActionSchedule struct {
	ID                 string           `json:"id"`
	Name               string           `json:"name"`
	KNID               string           `json:"kn_id"`
	Branch             string           `json:"branch"`
	ActionTypeID       string           `json:"action_type_id"`
	CronExpression     string           `json:"cron_expression"`
	InstanceIdentities []map[string]any `json:"_instance_identities,omitempty"`
	DynamicParams      map[string]any   `json:"dynamic_params,omitempty"`
	Status             string           `json:"status"`
	LastRunTime        int64            `json:"last_run_time,omitzero"`
	NextRunTime        int64            `json:"next_run_time,omitzero"`
	LockHolder         string           `json:"lock_holder,omitempty"`
	LockTime           int64            `json:"lock_time,omitzero"`
	Creator            AccountInfo      `json:"creator,omitempty"`
	CreateTime         int64            `json:"create_time,omitzero"`
	Updater            AccountInfo      `json:"updater,omitempty"`
	UpdateTime         int64            `json:"update_time,omitzero"`
	ExecutionSubject   AccountInfo      `json:"execution_subject,omitempty"`
	Source             string           `json:"source,omitempty"`
}

// PlatformKept reports whether the platform keeps the schedule for an action type.
func (s *ActionSchedule) PlatformKept() bool {
	return s != nil && s.Source == ActionScheduleSourceActionType
}

// ActionScheduleCreateRequest represents the request to create a schedule
type ActionScheduleCreateRequest struct {
	Name               string           `json:"name"`
	ActionTypeID       string           `json:"action_type_id"`
	CronExpression     string           `json:"cron_expression"`
	InstanceIdentities []map[string]any `json:"_instance_identities"`
	DynamicParams      map[string]any   `json:"dynamic_params,omitempty"`
	Status             string           `json:"status,omitempty"` // defaults to "inactive"
}

// ActionScheduleUpdateRequest represents the request to update a schedule
type ActionScheduleUpdateRequest struct {
	Name               string           `json:"name,omitempty"`
	CronExpression     string           `json:"cron_expression,omitempty"`
	InstanceIdentities []map[string]any `json:"_instance_identities,omitempty"`
	DynamicParams      map[string]any   `json:"dynamic_params,omitempty"`
}

// ActionScheduleStatusRequest represents the request to update schedule status
type ActionScheduleStatusRequest struct {
	Status string `json:"status"` // "active" or "inactive"
}

// ActionScheduleQueryParams represents query parameters for listing schedules
type ActionScheduleQueryParams struct {
	PaginationQueryParameters
	KNID         string
	Branch       string
	NamePattern  string
	ActionTypeID string
	Status       string
	// Source keeps the schedules of the source; "" lists every schedule.
	Source string
}

// ActionScheduleLockInfo represents lock acquisition info
type ActionScheduleLockInfo struct {
	ScheduleID string
	LockHolder string
	LockTime   int64
}

var (
	ACTION_SCHEDULE_SORT = map[string]string{
		"create_time":   "f_create_time",
		"update_time":   "f_update_time",
		"next_run_time": "f_next_run_time",
		"last_run_time": "f_last_run_time",
		"name":          "f_name",
	}
)

// GenerateScheduleAuditObject generates audit object for schedule
func GenerateScheduleAuditObject(scheduleID, scheduleName string) audit.AuditObject {
	return audit.AuditObject{
		Type: MODULE_TYPE_ACTION_SCHEDULE,
		ID:   scheduleID,
		Name: scheduleName,
	}
}
