// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

// States of an object type's local index, which full-text and vector search on its table need.
const (
	INDEX_STATE_READY = "ready"
	// INDEX_STATE_STALE is an index built before the table's configuration or structure changed.
	INDEX_STATE_STALE    = "stale"
	INDEX_STATE_BUILDING = "building"
	// INDEX_STATE_NOT_BUILT is a table configured for an index that has none yet.
	INDEX_STATE_NOT_BUILT = "not_built"
	// INDEX_STATE_NOT_CONFIGURED is a table missing fields a build needs; Missing names them.
	INDEX_STATE_NOT_CONFIGURED = "not_configured"
	INDEX_STATE_UNREADABLE     = "unreadable"
)

// Fields of a table's index configuration that a build needs.
const (
	INDEX_FIELD_PRIMARY_KEY = "primary_key_fields"
	INDEX_FIELD_INCREMENTAL = "incremental_fields"
)

// ProjectIndex is the readiness of the local indexes of a draft's adopted object types.
type ProjectIndex struct {
	ObjectTypes []ProjectIndexItem `json:"object_types"`
	// Counts are the object types in each state.
	Counts map[string]int `json:"counts"`
	// TasksVisible is false when the caller may not list a catalog's build tasks: an index being
	// built then reads as not built.
	TasksVisible bool `json:"tasks_visible"`
}

type ProjectIndexItem struct {
	ID    string     `json:"id"`
	Name  string     `json:"name"`
	Table DraftTable `json:"table"`
	State string     `json:"state"`
	// Missing names the configuration fields a build needs that the table lacks.
	Missing []string `json:"missing,omitempty"`
	// FullRebuildOnly is a table whose index refreshes by its identity's fields, the object type
	// having no update field: refreshing it rebuilds all of it.
	FullRebuildOnly bool `json:"full_rebuild_only,omitempty"`
	// UpdateField is the object type's update field and how to choose one.
	UpdateField ProjectIndexUpdateField `json:"update_field"`
	// Task is the build task under way.
	Task *ProjectIndexTask `json:"task,omitempty"`
	// Build is what the last confirmation did to build the table's index.
	Build *ProjectIndexBuild `json:"build,omitempty"`
}

type ProjectIndexBuild struct {
	// Outcome is one of the INDEX_BUILD_* outcomes.
	Outcome string `json:"outcome"`
	TaskID  string `json:"task_id,omitempty"`
	// Reason says why the build could not start.
	Reason string `json:"reason,omitempty"`
	Time   int64  `json:"time"`
}

type ProjectIndexTask struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// ProjectIndexUpdateField is an object type's update field, the property whose value grows whenever a
// record changes, which refreshes the table's index by what changed. Property is the one chosen,
// empty when none. Candidates are the properties holding a date or a time: none means no field of
// the table can be one, and the index is only ever rebuilt in full. Pending is set while the
// project's pending items ask for one, as the item of kind object_types, this object type's ID and
// reason needs_update_field; choosing one takes effect once the draft is confirmed again.
type ProjectIndexUpdateField struct {
	Property   string   `json:"property,omitempty"`
	Candidates []string `json:"candidates"`
	Pending    bool     `json:"pending"`
}
