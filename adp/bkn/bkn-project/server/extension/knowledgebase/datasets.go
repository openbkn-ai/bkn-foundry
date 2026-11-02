// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledgebase

import (
	"context"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// Datasets prepares only resources in the configured dedicated storage catalog.
// Ensure must read and verify ownership and the complete layout after create,
// conflict or uncertain completion; 409 alone is never successful preparation.
// Each operation rechecks that the storage service account is enabled. Core
// independently guards operator/source/model state before every call and bind.
type Datasets interface {
	Catalog(context.Context, string) (string, error)
	Ensure(context.Context, interfaces.DraftDatasetSpec) error
}

// DatasetRecords replaces one complete logical row at its stable identity.
// Core revalidates the active task, current source grant and model before each
// write. An uncertain response is an error; replay uses the identical row id.
// Ready resources must already have been prepared and verified by the model.
type DatasetRecords interface {
	Replace(context.Context, interfaces.DraftDatasetSpec, map[string]any) error
}
