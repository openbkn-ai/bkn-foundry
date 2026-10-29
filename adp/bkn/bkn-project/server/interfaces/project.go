// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

// A project changes one knowledge network on its own branch of the network's
// lineage, project/<project id>. Several projects may change one network; they
// share its lineage. The creator owns the project.

// Project list paging.
const (
	DEFAULT_PROJECT_LIMIT = 20
	MAX_PROJECT_LIMIT     = 100
)

// Project archive filters of the project list.
const (
	PROJECT_ARCHIVED_FALSE = "false"
	PROJECT_ARCHIVED_TRUE  = "true"
	PROJECT_ARCHIVED_ALL   = "all"
)

// Project is one row of t_bkn_project_project.
type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// KNID is the network the project changes, and the id of its lineage.
	KNID   string `json:"kn_id"`
	Branch string `json:"branch"`
	// BaselineNetworkID is the network that was live when the project started.
	BaselineNetworkID string   `json:"baseline_network_id"`
	CatalogIDs        []string `json:"catalog_ids"`
	// NamingLanguage is the language the project names its ontology items in.
	NamingLanguage string      `json:"naming_language"`
	Archived       bool        `json:"archived"`
	Owner          AccountInfo `json:"owner"`
	CreateTime     int64       `json:"create_time"`
	UpdateTime     int64       `json:"update_time"`
}

// Naming languages of a project.
const (
	NAMING_LANGUAGE_ZH_CN = "zh-CN"
	NAMING_LANGUAGE_EN_US = "en-US"
)

// IsNamingLanguage tells whether language is a naming language.
func IsNamingLanguage(language string) bool {
	return language == NAMING_LANGUAGE_ZH_CN || language == NAMING_LANGUAGE_EN_US
}

// ProjectCursor is the keyset position of the last project of a page.
type ProjectCursor struct {
	CreateTime int64  `json:"create_time"`
	ID         string `json:"id"`
}

// ProjectsQuery selects projects newest first. Only projects of KNIDs are
// selected; an empty KNIDs selects none.
type ProjectsQuery struct {
	KNIDs []string
	// Archived is nil for archived and unarchived projects alike.
	Archived *bool
	// Name matches project names containing it.
	Name   string
	Cursor *ProjectCursor
	Limit  int
}
