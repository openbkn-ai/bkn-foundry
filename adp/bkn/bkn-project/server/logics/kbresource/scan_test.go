// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbresource

import (
	"context"
	"errors"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"testing"
)

func TestMissingCatalogPreservesConstructionNotFoundSemantics(t *testing.T) {
	files, err := ScanFiles(context.Background(), &sourceStub{}, []interfaces.KBSourceRange{{KnowledgeBaseID: "1", CatalogID: "missing"}})
	var missing *MissingCatalogError
	var upstream *kb.SourceError
	if files != nil || !errors.As(err, &missing) || missing.KnowledgeBaseID != "1" || missing.CatalogID != "missing" || !errors.As(err, &upstream) || upstream.StatusCode != 404 {
		t.Fatal("missing catalog lost its exact scope or existing source status", err)
	}
}
