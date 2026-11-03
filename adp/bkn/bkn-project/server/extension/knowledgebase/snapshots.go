// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledgebase

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// DatasetFileSnapshot reads a complete, verified source-file snapshot for
// linking and fusion. Partial pages, duplicate rows or a different source/model
// scope fail the whole read. The guard runs before every remote page. Numeric
// values must retain their exact JSON representation, including large integers.
// No caller-supplied SQL or filter expression crosses this boundary.
type DatasetFileSnapshot interface {
	ReadFileRows(context.Context, interfaces.DraftDatasetSpec, DatasetFileScope, func(context.Context) error) ([]map[string]any, error)
}

// DatasetEntityRecords reads and prunes only the prepared unpublished generation
// and exact model. Full builds own canonical rows; sample builds only preview
// their fusion plan and must never replace full-generation current values.
type DatasetEntityRecords interface {
	DatasetRecords
	ReadEntityRows(context.Context, interfaces.DraftDatasetSpec, string, func(context.Context) error) ([]map[string]any, error)
	PruneEntities(context.Context, interfaces.DraftDatasetSpec, string, []string, func(context.Context) error) error
}
