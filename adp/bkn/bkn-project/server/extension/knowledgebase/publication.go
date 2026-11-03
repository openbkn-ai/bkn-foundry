// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledgebase

import (
	"context"
	"errors"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

var ErrPublishedModelMissing = errors.New("published model row not found")
var ErrPublishedBindingsChanged = errors.New("published network bindings changed")

// PublishedModelCheck verifies the actual network schema still consumes the
// fixed model's resources and fields. Publication metadata alone is not proof
// that another editor has left that live network's bindings unchanged.
type PublishedModelCheck interface {
	CheckPublishedBindings(context.Context, *interfaces.KBPublication, *interfaces.Draft) error
}

// PublicationDatasets keeps fixed execution models private to the configured
// storage application. It never grants raw model evidence to a project owner.
// Missing rows are distinct from failed or incomplete reads; only that explicit
// result permits the first immutable snapshot write.
type PublicationDatasets interface {
	Datasets
	DatasetRecords
	PublicationOwner(context.Context) (interfaces.AccountInfo, error)
	ReadPublishedModel(context.Context, interfaces.DraftDatasetSpec, string) (map[string]any, error)
}

// NetworkPublication mounts only the selected knowledge bases through the
// existing backend capability API, acting as the current publishing operator.
type NetworkPublication interface {
	MountKnowledgeBases(context.Context, string, string, []string) error
}
