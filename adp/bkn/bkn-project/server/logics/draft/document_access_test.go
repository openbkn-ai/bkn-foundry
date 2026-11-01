// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"errors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"testing"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/licverify"
)

type documentReadSource struct {
	kb.UnavailableSource
	calls         int
	denied        bool
	authorization string
}

func (s *documentReadSource) SummarizeFiles(ctx context.Context, scope kb.SourceScope, q kb.FileQuery) (kb.FileSummary, error) {
	s.calls++
	s.authorization = kb.OperatorAuthorization(ctx)
	if s.denied {
		return kb.FileSummary{}, &kb.SourceError{StatusCode: 403}
	}
	return kb.FileSummary{}, nil
}
func TestDocumentReadRequiresCurrentKBPermissionAndLeavesTableDraftsAlone(t *testing.T) {
	for _, scenario := range []string{"table", "allowed", "revoked", "unlicensed"} {
		t.Run(scenario, func(t *testing.T) {
			kb.ResetForTest()
			entitlement.ResetForTest()
			defer kb.ResetForTest()
			defer entitlement.ResetForTest()
			source := &documentReadSource{denied: scenario == "revoked"}
			edition := licverify.EditionProfessional
			if scenario == "unlicensed" {
				edition = licverify.EditionCommunity
			}
			entitlement.SetGate(entitlement.GateFunc(func() entitlement.Snapshot { return entitlement.Snapshot{Edition: edition} }))
			kb.Register(kb.Providers{Source: source, Concepts: kb.UnavailableConcepts{}})
			draft := &interfaces.Draft{}
			if scenario != "table" {
				q := interfaces.DraftSourceCitation{KnowledgeBaseID: "2", Quote: "fictional source"}
				draft.Items.ObjectTypes = []*interfaces.DraftObjectType{{Document: &interfaces.DraftDocumentModel{Citations: []interfaces.DraftSourceCitation{q}}, Properties: []*interfaces.DraftProperty{{Extraction: &interfaces.DraftExtractionField{Examples: []interfaces.DraftSourceCitation{q}}}}}}
			}
			err := checkDocumentReadAccess(context.Background(), "Bearer synthetic", draft)
			if scenario == "table" {
				if err != nil || source.calls != 0 {
					t.Fatal("table draft acquired a new dependency")
				}
				return
			}
			if scenario == "unlicensed" {
				if err == nil || source.calls != 0 {
					t.Fatal("unlicensed document evidence exposed")
				}
				return
			}
			if source.calls != 1 || source.authorization != "Bearer synthetic" {
				t.Fatal("duplicate calls or wrong identity")
			}
			if scenario == "revoked" {
				var denied *httperrors.HTTPError
				if !errors.As(err, &denied) || denied.HTTPCode != 403 {
					t.Fatal("revoked source draft not denied")
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}
