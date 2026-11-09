// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

// A release lineage is the history of one versioned knowledge network, whose id is the lineage
// id. A release is prepared first: its definitions are written into the network's draft, where the
// project's scenario questions are answered. Publishing it then turns the draft into the network's
// running version, numbered by bkn-backend (KNVersion) and labelled v<version>.

// Release states.
const (
	RELEASE_STATE_PREPARED  = "prepared"
	RELEASE_STATE_PUBLISHED = "published"
	// RELEASE_STATE_LEGACY is a release of the one-network-per-release scheme, kept as a record.
	RELEASE_STATE_LEGACY = "legacy"
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

// Release is one row of t_bkn_project_release, with its state worked out.
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
	// KNVersion is the network version publishing made; 0 while prepared.
	KNVersion int `json:"kn_version"`
	// BranchCommit holds the definitions prepared; publishing tags it v<version>.
	BranchCommit string `json:"branch_commit"`
	// DraftRevision is the draft's revision once the definitions were written into it.
	DraftRevision int64 `json:"-"`
	// Capabilities are the function and Skill files, release.yaml among them, publishing commits to main.
	Capabilities map[string][]byte `json:"-"`
	CreateTime   int64             `json:"create_time"`
	UpdateTime   int64             `json:"update_time"`
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
	// State selects one state; empty selects every release.
	State string
	Limit int
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
