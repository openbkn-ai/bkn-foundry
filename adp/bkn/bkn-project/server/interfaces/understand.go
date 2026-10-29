// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

// UNDERSTAND_NOT_STARTED is a table no semantic understanding task was started for; otherwise a
// table's status is its newest task's (pending, running, completed, failed or cancelled).
const UNDERSTAND_NOT_STARTED = "not_started"

// ProjectUnderstand asks to understand the tables of a project's data catalogs.
type ProjectUnderstand struct {
	// ApplyMode is fill_empty (the default when empty) or force.
	ApplyMode string `json:"apply_mode"`
	// All understands every table again; otherwise only the tables neither understood nor under way.
	All bool `json:"all"`
}

// ProjectUnderstandSubmitted is what an understanding submitted, by catalog.
type ProjectUnderstandSubmitted struct {
	Catalogs []UnderstandSubmittedCatalog `json:"catalogs"`
}

type UnderstandSubmittedCatalog struct {
	CatalogID string `json:"catalog_id"`
	// Permitted is false when the caller may not run tasks on the catalog (task_manage): nothing
	// is submitted there.
	Permitted bool `json:"permitted"`
	// Tables counts the tables submitted.
	Tables int `json:"tables"`
}

// ProjectUnderstanding is the semantic understanding of the tables of a project's data catalogs.
type ProjectUnderstanding struct {
	Catalogs []UnderstandingCatalog `json:"catalogs"`
}

type UnderstandingCatalog struct {
	CatalogID string `json:"catalog_id"`
	// Visible is false when the caller may not see the catalog's tasks (task_manage): its
	// progress is unknown.
	Visible bool `json:"visible"`
	// Counts are the tables in each status.
	Counts map[string]int       `json:"counts"`
	Tables []UnderstandingTable `json:"tables,omitempty"`
}

type UnderstandingTable struct {
	ResourceID string `json:"resource_id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	// Applied tells whether the newest task wrote its result to the table.
	Applied    bool  `json:"applied,omitempty"`
	FinishTime int64 `json:"finish_time,omitempty"`
}
