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
	// Task is the build task under way.
	Task *ProjectIndexTask `json:"task,omitempty"`
}

type ProjectIndexTask struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}
