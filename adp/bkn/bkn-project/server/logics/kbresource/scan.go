// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbresource

import (
	"context"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// ScanFiles shares the complete, filtered source enumeration with published
// refresh without reading a mutable project or its selection. The source retains
// responsibility for current operator/proxy authorization on every request.
func ScanFiles(ctx context.Context, source kb.Source, ranges []interfaces.KBSourceRange) ([]interfaces.KBResourceFile, error) {
	if source == nil {
		return nil, kb.ErrUnavailable
	}
	return (&Service{source: source}).files(ctx, ranges)
}

// MissingCatalogError is produced only after a complete, authorized catalog list
// confirms that this exact selected catalog is absent. It preserves K1's 404
// semantics while letting refresh retire a deleted range rather than confusing
// an unavailable or forbidden KB with an empty directory.
type MissingCatalogError struct{ KnowledgeBaseID, CatalogID string }

func (*MissingCatalogError) Error() string { return "selected knowledge-base catalog no longer exists" }
func (*MissingCatalogError) Unwrap() error { return &kb.SourceError{StatusCode: 404} }
