// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package docdraft

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbconcept"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbdataset"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
)

func adoptedDocuments(draft *interfaces.Draft) bool {
	for _, object := range draft.Items.ObjectTypes {
		if object != nil && object.Adopted && object.Document != nil {
			return true
		}
	}
	return false
}

func datasetNotReady(ctx context.Context, status int) error {
	return httperrors.NewHTTPError(ctx, status, perrors.BknProject_Draft_DocumentDataNotReady)
}

// prepareDocumentDatasets uses the same project/source/draft transaction as
// candidate adoption. Process metadata is durable before resource creation;
// no token, source text or instance rows are added to the journal.
func (s service) prepareDocumentDatasets(ctx context.Context, token string, entry *interfaces.ProjectEntry,
	draft *interfaces.Draft, caller interfaces.AccountInfo) (*interfaces.Draft, error) {
	if !adoptedDocuments(draft) {
		return draft, nil
	}
	providers, enabled := kb.Lookup()
	if !enabled || providers.Datasets == nil {
		return nil, datasetNotReady(ctx, http.StatusConflict)
	}
	access, ok := kbconcept.DocumentDrafts(s.DraftAccess())
	if !ok || s.resources == nil {
		return nil, datasetNotReady(ctx, http.StatusServiceUnavailable)
	}
	state, err := s.resources.Read(ctx, draft.ProjectID)
	if err != nil {
		return nil, datasetNotReady(ctx, http.StatusServiceUnavailable)
	}
	if state == nil || state.Current == nil || state.Current.State != "active" || state.Current.Owner != caller {
		return nil, datasetNotReady(ctx, http.StatusConflict)
	}
	selection := *state.Current
	fingerprint, err := kbdataset.ModelFingerprint(draft)
	if err != nil {
		return nil, datasetNotReady(ctx, http.StatusConflict)
	}
	validate := func(checkCtx context.Context, value *interfaces.Draft) error {
		project, err := s.UnarchivedProject(checkCtx, token, draft.ProjectID)
		if err != nil {
			return err
		}
		if project.Owner != caller {
			return httperrors.NewHTTPError(checkCtx, http.StatusForbidden, perrors.BknProject_Draft_NotOwner)
		}
		current, err := s.resources.Read(checkCtx, draft.ProjectID)
		if err != nil {
			return err
		}
		if current == nil || current.Current == nil || current.Current.ID != selection.ID || current.Current.Revision != selection.Revision || current.Current.State != "active" || current.Current.Owner != caller {
			return kbdataset.ErrStalePreparation
		}
		actual, found, err := s.DraftAccess().GetDraft(checkCtx, draft.ProjectID)
		if err != nil {
			return err
		}
		if !found || actual.Status != interfaces.DRAFT_STATUS_READY || actual.UpdateTime != value.UpdateTime {
			return kbdataset.ErrStalePreparation
		}
		actualFingerprint, err := kbdataset.ModelFingerprint(actual)
		if err != nil || actualFingerprint != fingerprint {
			return kbdataset.ErrStalePreparation
		}
		// Check every selected KB, even when only technical evidence templates were
		// adopted or a model carries no direct source citation. This reads metadata,
		// never the heldout documents' contents.
		seen := map[string]bool{}
		for _, source := range current.Current.Ranges {
			if seen[source.KnowledgeBaseID] {
				continue
			}
			seen[source.KnowledgeBaseID] = true
			if _, err := providers.Source.SummarizeFiles(kb.WithOperatorAuthorization(checkCtx, token), kb.SourceScope{KnowledgeBaseID: source.KnowledgeBaseID}, kb.FileQuery{}); err != nil {
				return err
			}
		}
		return nil
	}
	if err = validate(ctx, draft); err != nil {
		return nil, datasetPreparationError(ctx, err)
	}
	catalog, err := providers.Datasets.Catalog(ctx, draft.ProjectID)
	if err != nil {
		return nil, datasetNotReady(ctx, http.StatusServiceUnavailable)
	}
	var plan *interfaces.DraftDatasetPlan
	for i := len(draft.Items.DatasetPlans) - 1; i >= 0; i-- {
		candidate := draft.Items.DatasetPlans[i]
		if candidate != nil && candidate.Owner == caller && candidate.ModelFingerprint == fingerprint && candidate.SelectionID == selection.ID && candidate.SelectionRevision == selection.Revision {
			plan = candidate
			break
		}
	}
	if plan == nil {
		plan, err = kbdataset.NewPlan(draft, uuid.NewString(), catalog, caller)
		if err != nil {
			return nil, datasetNotReady(ctx, http.StatusConflict)
		}
	}
	if plan.SelectionID != selection.ID || plan.SelectionRevision != selection.Revision {
		return nil, datasetNotReady(ctx, http.StatusConflict)
	}
	for _, spec := range plan.Datasets {
		if spec.CatalogID != catalog {
			return nil, datasetNotReady(ctx, http.StatusConflict)
		}
	}
	// Run the existing BKN compiler before creating any remote resource. The
	// preview is independent and is never exposed or committed as a partial bind.
	preview, err := kbdataset.BindPreview(draft, plan)
	if err != nil {
		return nil, datasetNotReady(ctx, http.StatusConflict)
	}
	if err = s.CheckCompiles(&preview.Items); err != nil {
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidItem)
	}
	currentAt := draft.UpdateTime
	save := func(writeCtx context.Context, value *interfaces.Draft) error {
		old := value.UpdateTime
		value.UpdateTime = max(time.Now().UnixMilli(), currentAt+1)
		changed, err := access.CommitDocumentDraft(writeCtx, value, interfaces.DocumentDraftGuard{Owner: caller, SelectionID: selection.ID, SelectionRevision: selection.Revision, DraftExists: true, DraftUpdateTime: currentAt})
		if err != nil || !changed {
			value.UpdateTime = old
			if err != nil {
				return err
			}
			return kbdataset.ErrStalePreparation
		}
		currentAt = value.UpdateTime
		return nil
	}
	err = kbdataset.Prepare(ctx, draft, plan, providers.Datasets, save, validate)
	if err != nil {
		return nil, datasetPreparationError(ctx, err)
	}

	return draft, nil
}

func datasetPreparationError(ctx context.Context, err error) error {
	if errors.Is(err, kbdataset.ErrInvalidLayout) || errors.Is(err, kbdataset.ErrStalePreparation) {
		return datasetNotReady(ctx, http.StatusConflict)
	}
	var upstream *kb.SourceError
	if errors.As(err, &upstream) {
		return documentSourceError(ctx, err)
	}
	var problem *httperrors.HTTPError
	if errors.As(err, &problem) {
		return err
	}
	return datasetNotReady(ctx, http.StatusServiceUnavailable)
}

func DatasetPreparation(draft *interfaces.Draft, selection *interfaces.KBSelection) *interfaces.DatasetPreparation {
	if draft == nil || len(draft.Items.DatasetPlans) == 0 {
		return nil
	}
	fingerprint, err := kbdataset.ModelFingerprint(draft)
	if err != nil {
		return nil
	}
	for index := len(draft.Items.DatasetPlans) - 1; index >= 0; index-- {
		plan := draft.Items.DatasetPlans[index]
		if plan == nil || plan.ModelFingerprint != fingerprint || selection == nil || selection.State != "active" || plan.SelectionID != selection.ID || plan.SelectionRevision != selection.Revision || plan.Owner != selection.Owner {
			continue
		}
		result := &interfaces.DatasetPreparation{State: plan.State, Total: len(plan.Datasets)}
		for _, spec := range plan.Datasets {
			if spec.Ready {
				result.Ready++
			}
		}
		// Preparing is synchronous and bounded by the server request timeout.
		// A process that disappeared cannot leave the UI polling indefinitely.
		if result.State == "preparing" && time.Now().UnixMilli()-draft.UpdateTime > int64(10*time.Minute/time.Millisecond) {
			result.State = "failed"
		}
		return result
	}
	return &interfaces.DatasetPreparation{State: "stale"}
}
