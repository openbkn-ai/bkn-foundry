// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "context"

// ModelGitStore atomically commits BKN definitions and the server-projected
// document rules in project/knowledge-base.json. It preserves other project
// metadata and capability artifacts. Nil rules removes only that owned file.
// Raw evidence, process decisions and runtime snapshots are never arguments.
type ModelGitStore interface {
	ReadModelRules(context.Context, string, string) ([]byte, error)
	CommitModel(context.Context, string, string, map[string][]byte, []byte, string, GitAuthor) (string, error)
}

// ModelLineageService confirms the two definition artifacts together. Older
// LineageService implementations remain valid for table-only drafts.
type ModelLineageService interface {
	AddModelDefinitions(context.Context, string, string, map[string][]byte, []byte, []string, string, GitAuthor) (*CommitDefinitionsResult, error)
}
