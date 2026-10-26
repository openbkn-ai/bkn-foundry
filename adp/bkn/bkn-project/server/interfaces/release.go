// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

// A release lineage is the chain of knowledge networks published from one base
// network. Each release is imported as a new network whose id is
// <base_id>_<version>; the lineage id is the base network id. Exactly one
// release of a lineage is live, the earlier ones are archived. The archived
// state is recorded here only; bkn-backend does not enforce read-only.

// Release states.
const (
	RELEASE_STATE_LIVE     = "live"
	RELEASE_STATE_ARCHIVED = "archived"
)

// Release list paging.
const (
	DEFAULT_RELEASE_LIMIT = 20
	MAX_RELEASE_LIMIT     = 100
)

// AccountInfo identifies an account the way bkn-backend stores creators and updaters.
type AccountInfo struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

// Release is one row of t_bkn_project_release.
type Release struct {
	ID          string      `json:"id"`
	LineageID   string      `json:"lineage_id"`
	NetworkID   string      `json:"network_id"`
	Version     string      `json:"version"`
	BaseVersion string      `json:"base_version"`
	ProjectID   string      `json:"project_id"`
	Publisher   AccountInfo `json:"publisher"`
	Notes       string      `json:"notes"`
	State       string      `json:"state"`
	CreateTime  int64       `json:"create_time"`
	UpdateTime  int64       `json:"update_time"`
}

// ReleaseCursor is the keyset position after which the next page starts.
// Releases are listed newest first: by create_time, then id, both descending.
type ReleaseCursor struct {
	CreateTime int64  `json:"t"`
	ID         string `json:"id"`
}

// ReleasesQuery selects one page of a lineage's releases.
type ReleasesQuery struct {
	LineageID string
	Limit     int
	// Cursor is nil for the first page.
	Cursor *ReleaseCursor
}

// ReleaseList is one page of a lineage's releases.
type ReleaseList struct {
	Entries []*Release `json:"entries"`
	Total   int64      `json:"total"`
	// NextCursor is nil on the last page.
	NextCursor *string `json:"next_cursor"`
}
