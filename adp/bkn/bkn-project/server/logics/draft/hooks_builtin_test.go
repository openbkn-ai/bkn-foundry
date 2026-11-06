// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/projecthooks"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/docdraft"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbconcept"
)

// The tests of document items run through the built-in hooks, their assertions as they were. These
// shims name what moved; they would leave with the tests.
func init() {
	projecthooks.SetForTest(projecthooks.Hooks{Drafts: docdraft.Hooks{}})
}

// useDocumentResources has the built-in hooks read the source selection given, for one test.
func useDocumentResources(t *testing.T, resources interfaces.KBResourceAccess) {
	t.Cleanup(projecthooks.SetForTest(projecthooks.Hooks{Drafts: docdraft.Hooks{Resources: resources}}))
}

func checkDocumentReadAccess(ctx context.Context, token string, draft *interfaces.Draft) error {
	return docdraft.CheckDocumentReadAccess(ctx, token, draft)
}

func applySourceFieldDecision(ctx context.Context, draft *interfaces.Draft, edited *interfaces.DraftObjectType, decision *interfaces.DualSourceDecision, caller interfaces.AccountInfo) error {
	return docdraft.ApplySourceFieldDecision(ctx, draft, edited, decision, caller)
}

func documentRelationRevision(ctx context.Context, input *kbconcept.SampleInput, items interfaces.DraftItems, base *interfaces.DraftRelationType, out projecthooks.AskRevisionOutput) (*interfaces.DraftAskRevision, string) {
	return docdraft.RelationRevision(ctx, draftHost{&draftService{}}, input, items, base, out)
}

func (s *draftService) verifyBusinessIdentity(ctx context.Context, draft *interfaces.Draft, relation *interfaces.DraftRelationType, caller interfaces.AccountInfo) (*interfaces.DraftBusinessIdentityEvidence, error) {
	return docdraft.Hooks{}.VerifyBusinessIdentity(ctx, draftHost{s}, draft, relation, caller)
}

func checkSourceFieldDecisions(ctx context.Context, draft *interfaces.Draft) error {
	return docdraft.CheckSourceFieldDecisions(ctx, draft)
}

func (s *draftService) checkBusinessIdentities(ctx context.Context, draft *interfaces.Draft, caller interfaces.AccountInfo) error {
	return documentHooks().(docdraft.Hooks).CheckBusinessIdentities(ctx, draftHost{s}, draft, caller)
}

func (s *draftService) prepareDocumentDatasets(ctx context.Context, token string, entry *interfaces.ProjectEntry, draft *interfaces.Draft, caller interfaces.AccountInfo) (*interfaces.Draft, error) {
	return documentHooks().(docdraft.Hooks).PrepareDocumentDatasets(ctx, draftHost{s}, token, entry, draft, caller)
}

func datasetPreparation(draft *interfaces.Draft, selection *interfaces.KBSelection) *interfaces.DatasetPreparation {
	return docdraft.DatasetPreparation(draft, selection)
}

func documentExportItems(items *interfaces.DraftItems, evidence *interfaces.DraftEvidenceExport) (*interfaces.DraftItems, error) {
	return documentHooks().ExportItems(items, evidence)
}
