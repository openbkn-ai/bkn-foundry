// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/projecthooks"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// documentHooks is what document items add to a draft; none, until an edition registers them.
func documentHooks() projecthooks.Drafts {
	if hooks := projecthooks.Current().Drafts; hooks != nil {
		return hooks
	}
	return noDocumentHooks{}
}

// draftHost is the draft service as the hooks reach it.
type draftHost struct{ s *draftService }

func (h draftHost) Snapshot(ot *interfaces.DraftObjectType) interfaces.DraftObjectTypeSnapshot {
	return snapshot(ot)
}
func (h draftHost) SnapshotsEqual(a, b interfaces.DraftObjectTypeSnapshot) bool {
	return snapshotsEqual(a, b)
}
func (h draftHost) RelationSnapshot(rt *interfaces.DraftRelationType) interfaces.DraftRelationTypeSnapshot {
	return relationSnapshot(rt)
}
func (h draftHost) SameJoin(a, b interfaces.DraftRelationTypeSnapshot) bool { return sameJoin(a, b) }
func (h draftHost) ApplyEdit(ctx context.Context, items *interfaces.DraftItems, kind, id string, edit interfaces.DraftItemEdit) ([]*interfaces.DraftDecision, error) {
	return applyEdit(ctx, items, kind, id, edit)
}
func (h draftHost) CommitUsing(ctx context.Context, draft *interfaces.Draft, decisions []*interfaces.DraftDecision, caller interfaces.AccountInfo, write projecthooks.CommitWrite) error {
	return h.s.commitUsing(ctx, draft, decisions, caller, write)
}
func (h draftHost) InternalError(ctx context.Context, operation string, err error) error {
	return internalError(ctx, operation, err)
}
func (h draftHost) InvalidRelation(ctx context.Context, details map[string]any) error {
	return invalidRelation(ctx, details)
}
func (h draftHost) NameOrComment(ctx context.Context, field, value, name, description string) (*interfaces.DraftAskRevision, string) {
	return nameOrComment(ctx, field, value, current(field, name, description))
}
func (h draftHost) UnarchivedProject(ctx context.Context, token, projectID string) (*interfaces.ProjectEntry, error) {
	return h.s.unarchivedProject(ctx, token, projectID)
}
func (h draftHost) CheckCompiles(items *interfaces.DraftItems, evidence *interfaces.DraftEvidenceExport) error {
	_, err := compileDraft(items, evidence)
	return err
}
func (h draftHost) DraftAccess() interfaces.DraftAccess { return h.s.da }

// CountBusinessKey counts a field of a table as a key, as the caller.
func (h draftHost) CountBusinessKey(ctx context.Context, caller interfaces.AccountInfo, resourceID, mappedField, fieldType string) projecthooks.BusinessKeyCount {
	s := h.s
	if s.va == nil {
		return projecthooks.BusinessKeyCount{Refusal: projecthooks.KeyInvalid}
	}
	countCtx, cancel := context.WithTimeout(ctx, identityCountTimeout)
	defer cancel()
	tables, err := s.va.GetTables(countCtx, caller, []string{resourceID})
	if err != nil {
		return projecthooks.BusinessKeyCount{Refusal: projecthooks.KeyUnavailable}
	}
	if len(tables) != 1 || tables[0] == nil || tables[0].ID != resourceID {
		return projecthooks.BusinessKeyCount{Refusal: projecthooks.KeyUnavailable}
	}
	physical, found := column(tables[0], mappedField)
	if !found || bknTypes[physical.Type] != fieldType {
		return projecthooks.BusinessKeyCount{Refusal: projecthooks.KeyInvalid}
	}
	timeout := s.setting.QueryTimeout
	if timeout <= 0 || timeout > int(identityCountTimeout.Seconds()) {
		timeout = int(identityCountTimeout.Seconds())
	}
	counter := &vegaCounter{va: s.va, account: caller, timeoutSeconds: timeout}
	rows, distinct, nulls, blanks, err := counter.businessKeyStats(countCtx, tables[0], mappedField, fieldType)
	if err != nil {
		return projecthooks.BusinessKeyCount{Refusal: projecthooks.KeyUnavailable}
	}
	return projecthooks.BusinessKeyCount{Rows: rows, Distinct: distinct, Nulls: nulls, Blanks: blanks}
}

// noDocumentHooks is the community default. It owns no item: a draft holding document items, which
// only an edition with the hooks can make, is refused as a source out of reach.
type noDocumentHooks struct{}

var _ projecthooks.Drafts = noDocumentHooks{}

func noDocuments(ctx context.Context) error {
	return httperrors.NewHTTPError(ctx, http.StatusServiceUnavailable, perrors.BknProject_Draft_SourceUnreachable)
}

func (noDocumentHooks) CheckRead(ctx context.Context, _ string, draft *interfaces.Draft) error {
	if draft == nil {
		return nil
	}
	for _, o := range draft.Items.ObjectTypes {
		if o != nil && o.Document != nil {
			return noDocuments(ctx)
		}
	}
	for _, r := range draft.Items.RelationTypes {
		if r != nil && r.Document != nil {
			return noDocuments(ctx)
		}
	}
	return nil
}
func (noDocumentHooks) ProjectRead(*interfaces.DraftItems) {}
func (noDocumentHooks) SnapshotObject(*interfaces.DraftObjectType, *interfaces.DraftObjectTypeSnapshot) {
}
func (noDocumentHooks) SnapshotRelation(*interfaces.DraftRelationType, *interfaces.DraftRelationTypeSnapshot) {
}
func (noDocumentHooks) SaveObject(ctx context.Context, _ projecthooks.Host, _ string, _ *interfaces.Draft, ot *interfaces.DraftObjectType, save interfaces.DraftObjectTypeSave, _ interfaces.AccountInfo) (*interfaces.Draft, bool, error) {
	// A table item ignores document settings, as before; only deriving a document item needs them.
	if ot.Document != nil || (save.Document != nil && save.Document.Derive != nil) {
		return nil, true, noDocuments(ctx)
	}
	return nil, false, nil
}
func (noDocumentHooks) CreateRelation(ctx context.Context, _ projecthooks.Host, _ string, _ *interfaces.Draft, _ *interfaces.DraftRelationType, save interfaces.DraftRelationTypeSave, _ interfaces.AccountInfo) (*interfaces.Draft, bool, error) {
	if save.DocumentMapping != nil {
		return nil, true, noDocuments(ctx)
	}
	return nil, false, nil
}
func (noDocumentHooks) SaveRelation(ctx context.Context, _ projecthooks.Host, _ string, _ *interfaces.Draft, rt *interfaces.DraftRelationType, save interfaces.DraftRelationTypeSave, _ interfaces.AccountInfo) (*interfaces.Draft, bool, error) {
	if rt.Document != nil || save.DocumentMapping != nil {
		return nil, true, noDocuments(ctx)
	}
	return nil, false, nil
}
func (noDocumentHooks) CheckRelationDelete(context.Context, *interfaces.DraftItems, string) error {
	return nil
}
func (noDocumentHooks) CommitEdit(ctx context.Context, _ projecthooks.Host, _ *interfaces.Draft, _ []*interfaces.DraftDecision, _ interfaces.AccountInfo, model *interfaces.DraftDocumentModel) (bool, error) {
	if model != nil {
		return true, noDocuments(ctx)
	}
	return false, nil
}
func (noDocumentHooks) CheckIdentity(ctx context.Context, _ *interfaces.DraftItems, ot *interfaces.DraftObjectType, _ []string) error {
	if ot.Document != nil {
		return noDocuments(ctx)
	}
	return nil
}
func (noDocumentHooks) SetIdentity(*interfaces.DraftObjectType, []string) ([]string, bool) {
	return nil, false
}
func (noDocumentHooks) AskObject(ctx context.Context, _ projecthooks.Host, _ string, _ *interfaces.Draft, original, working *interfaces.DraftObjectType) (projecthooks.AskObject, error) {
	if original.Document != nil {
		return projecthooks.AskObject{}, noDocuments(ctx)
	}
	return projecthooks.AskObject{Working: working}, nil
}
func (noDocumentHooks) AskAgentID(*interfaces.Draft) string { return "" }
func (noDocumentHooks) AskObjectContext(*interfaces.DraftObjectType) (any, map[string]string) {
	return nil, nil
}
func (noDocumentHooks) AskRelationContext(*interfaces.DraftRelationType) (any, *interfaces.DraftDocumentMapping) {
	return nil, nil
}
func (noDocumentHooks) AskRelation(ctx context.Context, _ projecthooks.Host, _ string, _ *interfaces.Draft, _, base *interfaces.DraftRelationType) (projecthooks.AskRelation, error) {
	if base.Document != nil {
		return projecthooks.AskRelation{}, noDocuments(ctx)
	}
	return projecthooks.AskRelation{Working: base}, nil
}
func (noDocumentHooks) AskNewRelation(ctx context.Context, _ projecthooks.Host, _ string, _ *interfaces.Draft, req interfaces.DraftRelationAskRequest) (projecthooks.AskRelation, error) {
	if req.Working != nil && req.Working.DocumentMapping != nil {
		return projecthooks.AskRelation{}, noDocuments(ctx)
	}
	return projecthooks.AskRelation{}, nil
}
func (noDocumentHooks) AskOfferable(*interfaces.DraftObjectType) bool { return true }
func (noDocumentHooks) RevisionSchema(int) map[string]any             { return nil }
func (noDocumentHooks) WorkingObject(*interfaces.DraftObjectType, *interfaces.DraftObjectTypeSave) {
}
func (noDocumentHooks) WorkingProperty(*interfaces.DraftObjectType, *interfaces.DraftProperty, interfaces.DraftDataPropertySave) {
}
func (noDocumentHooks) WorkingRelation(*interfaces.DraftRelationType, *interfaces.DraftRelationTypeSave) bool {
	return false
}
func (noDocumentHooks) IdentityRevisionAllowed(*interfaces.DraftObjectType, []string, []*interfaces.DraftRelationType) bool {
	return true
}
func (noDocumentHooks) InstructionRevision(*interfaces.DraftObjectType, *interfaces.DraftProperty, *string) (*string, string) {
	return nil, projecthooks.AskDroppedNotEditable
}
func (noDocumentHooks) CheckConfirm(context.Context, *interfaces.Draft) error { return nil }
func (noDocumentHooks) PrepareConfirm(_ context.Context, _ projecthooks.Host, _ string, _ *interfaces.ProjectEntry, draft *interfaces.Draft, _ interfaces.AccountInfo) (*interfaces.Draft, error) {
	return draft, nil
}
func (noDocumentHooks) DatasetProgress(context.Context, projecthooks.Host, *interfaces.Draft) {}
func (noDocumentHooks) ObjectNeedsRepair(*interfaces.DraftItems, *interfaces.DraftObjectType) bool {
	return false
}
func (noDocumentHooks) CheckAdoptRelation(context.Context, *interfaces.DraftItems, *interfaces.DraftRelationType) error {
	return nil
}
func (noDocumentHooks) ExportItems(items *interfaces.DraftItems, _ *interfaces.DraftEvidenceExport) (*interfaces.DraftItems, error) {
	return items, nil
}
func (noDocumentHooks) ConfirmEvidence(context.Context, projecthooks.Host, *interfaces.Draft) (*interfaces.DraftEvidenceExport, error) {
	return nil, nil
}
func (noDocumentHooks) DefinitionRules(context.Context, *interfaces.Draft, bool) ([]byte, error) {
	return nil, nil
}
