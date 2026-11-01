// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbconcept"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
)

func documentEditError(ctx context.Context) error {
	return httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidProperty)
}
func documentValidationError(ctx context.Context, err error) error {
	var scope *kbconcept.RelationScopeError
	if errors.As(err, &scope) {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_DocumentRelationScope).
			WithMetadata(map[string]any{"name": scope.Name}).
			WithErrorDetails(map[string]any{"relation_id": scope.ID, "relation_name": scope.Name})
	}
	return documentEditError(ctx)
}
func (s *draftService) documentEditInput(ctx context.Context, token string, draft *interfaces.Draft, model *interfaces.DraftDocumentModel) (*kbconcept.SampleInput, error) {
	providers, enabled := kb.Lookup()
	if !enabled || s.kbResources == nil {
		return nil, httperrors.NewHTTPError(ctx, 503, perrors.BknProject_Draft_SourceUnreachable)
	}
	state, err := s.kbResources.Read(ctx, draft.ProjectID)
	if err != nil {
		return nil, internalError(ctx, "Read document source selection failed", err)
	}
	if state == nil || state.Current == nil || state.Current.State != "active" || state.Current.ID != model.SelectionID || state.Current.Revision != model.SelectionRevision {
		return nil, httperrors.NewHTTPError(ctx, 409, perrors.BknProject_Draft_Stale)
	}
	source, ok := providers.Source.(kb.SampleSource)
	if !ok {
		return nil, httperrors.NewHTTPError(ctx, 503, perrors.BknProject_Draft_SourceUnreachable)
	}
	input, err := kbconcept.LoadSamples(kb.WithOperatorAuthorization(ctx, token), source, state.Current, draftLanguage(draft))
	if err != nil {
		status, key := 502, "SourceUnavailable"
		var upstream *kb.SourceError
		if errors.As(err, &upstream) && (upstream.StatusCode == 401 || upstream.StatusCode == 403 || upstream.StatusCode == 410 || upstream.StatusCode == 413 || upstream.StatusCode == 503) {
			status = upstream.StatusCode
		}
		if errors.Is(err, kb.ErrVersionGone) {
			status = 410
		}
		switch status {
		case 401:
			key = "AuthenticationExpired"
		case 403:
			key = "PermissionDenied"
		case 410:
			key = "VersionGone"
		case 413:
			key = "SampleLimit"
		}
		if errors.Is(err, kbconcept.ErrSampleLimit) {
			status, key = 413, "SampleLimit"
		}
		result := httperrors.NewHTTPError(ctx, status, perrors.BknProject_Draft_SourceUnreachable)
		if message, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknProject.KnowledgeBase."+key, nil); renderErr == nil {
			return nil, result.WithErrorDetails(message)
		}
		return nil, result
	}
	return input, nil
}
func (s *draftService) saveDocumentObject(ctx context.Context, token string, draft *interfaces.Draft, original *interfaces.DraftObjectType, save interfaces.DraftObjectTypeSave, caller interfaces.AccountInfo) (*interfaces.Draft, error) {
	if save.IncrementalKey != "" {
		return nil, documentEditError(ctx)
	}
	before := snapshot(original)
	beforeName, beforeDescription, beforeAdopted := original.Name, original.Description, original.Adopted
	raw, err := json.Marshal(original)
	if err != nil {
		return nil, documentEditError(ctx)
	}
	edited := &interfaces.DraftObjectType{}
	if json.Unmarshal(raw, edited) != nil {
		return nil, documentEditError(ctx)
	}
	edited.Name, edited.Description = strings.TrimSpace(save.Name), strings.TrimSpace(save.Comment)
	fixed := map[string]*interfaces.DraftProperty{}
	existing := map[string]*interfaces.DraftProperty{}
	for _, p := range original.Properties {
		existing[p.Name] = p
		if p.ReadOnly {
			fixed[p.Name] = p
		}
	}
	edited.Properties = nil
	seen := map[string]bool{}
	for _, p := range save.DataProperties {
		if seen[p.Name] || p.MappedField != nil {
			return nil, documentEditError(ctx)
		}
		seen[p.Name] = true
		if keep := fixed[p.Name]; keep != nil {
			if p.Type != keep.Type || p.DisplayName != keep.DisplayName || p.Comment != keep.Description || p.Extraction != nil {
				return nil, documentEditError(ctx)
			}
			continue
		}
		extraction := p.Extraction
		if extraction == nil && existing[p.Name] != nil {
			extraction = existing[p.Name].Extraction
		}
		edited.Properties = append(edited.Properties, &interfaces.DraftProperty{Name: p.Name, DisplayName: strings.TrimSpace(p.DisplayName), Description: strings.TrimSpace(p.Comment), Type: p.Type, Extraction: extraction})
	}
	// A missing managed field is preserved, never deleted or silently remapped.
	for _, p := range original.Properties {
		if p.ReadOnly {
			edited.Properties = append(edited.Properties, p)
		}
	}
	edited.PrimaryKeys = append([]string(nil), save.PrimaryKeys...)
	edited.DisplayKey = save.DisplayKey
	if save.Document != nil {
		edited.Document.TimeProperty = save.Document.TimeProperty
		edited.Document.ParticipantRelations = append([]string(nil), save.Document.ParticipantRelations...)
	}
	if strings.HasPrefix(original.Document.Role, "evidence_") {
		if !reflect.DeepEqual(edited.Properties, original.Properties) || !reflect.DeepEqual(edited.PrimaryKeys, original.PrimaryKeys) || edited.DisplayKey != original.DisplayKey || edited.Document.TimeProperty != "" || len(edited.Document.ParticipantRelations) > 0 {
			return nil, documentEditError(ctx)
		}
	} else {
		if original.Document.Role != "entity" && !reflect.DeepEqual(edited.PrimaryKeys, original.PrimaryKeys) {
			return nil, documentEditError(ctx)
		}
		input, err := s.documentEditInput(ctx, token, draft, original.Document)
		if err != nil {
			return nil, err
		}
		checked, err := kbconcept.ValidateEditedObject(input, draft.Items, original, edited)
		if err != nil {
			return nil, documentValidationError(ctx, err)
		}
		edited = checked
	}
	// No vega table scan or key counting applies to a document extraction schema.
	name, description := edited.Name, edited.Description
	*original = *edited
	original.Name, original.Description, original.Adopted = beforeName, beforeDescription, beforeAdopted
	edit := interfaces.DraftItemEdit{Name: &name, Description: &description}
	if beforeAdopted && !edited.Adopted {
		adopted := false
		edit.Adopted = &adopted
	}
	decisions, err := applyEdit(ctx, &draft.Items, interfaces.DRAFT_KIND_OBJECT_TYPES, original.ID, edit)
	if err != nil {
		return nil, err
	}
	after := snapshot(original)
	if !snapshotsEqual(before, after) {
		decisions = append(decisions, &interfaces.DraftDecision{Kind: interfaces.DRAFT_KIND_OBJECT_TYPES, ItemID: original.ID, Action: interfaces.DRAFT_DECISION_SAVE, Before: before, After: after})
	}
	if err = s.commitDocumentEdit(ctx, draft, decisions, caller, original.Document); err != nil {
		return nil, err
	}
	return draft, nil
}

func (s *draftService) commitDocumentEdit(ctx context.Context, draft *interfaces.Draft, decisions []*interfaces.DraftDecision, caller interfaces.AccountInfo, model *interfaces.DraftDocumentModel) error {
	if len(decisions) == 0 {
		return nil
	}
	writer, ok := s.da.(interfaces.DocumentDraftAccess)
	if !ok || model == nil {
		return httperrors.NewHTTPError(ctx, 503, perrors.BknProject_Draft_SourceUnreachable)
	}
	return s.commitUsing(ctx, draft, decisions, caller, func(ctx context.Context, draft *interfaces.Draft, readAt int64) (bool, error) {
		return writer.CommitDocumentDraft(ctx, draft, interfaces.DocumentDraftGuard{Owner: caller, SelectionID: model.SelectionID, SelectionRevision: model.SelectionRevision, DraftExists: true, DraftUpdateTime: readAt})
	})
}
