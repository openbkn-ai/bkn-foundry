// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package diagnosis

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/projecthooks"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// A suggestion is applied with the draft's own operations, each with its own checks: adopting a
// relation or an object type, leaving one out, making a relation; a write-back object goes through
// the edition's hook, and a concept's disposition is recorded on the requirement specification.
// Applying, and dismissing, keep the diagnosis current: the suggestion's status changes, and the
// draft update time the diagnosis holds for moves along with the change it made, so only changes
// made elsewhere make it stale.

// Why a suggestion cannot be applied directly, in the error details.
const (
	notApplicableChooser   = "opens_chooser"
	notApplicableNoApply   = "no_operation"
	notApplicableEdition   = "not_in_edition"
	notApplicableNotReady  = "diagnosis_not_ready"
	staleReasonChanged     = "draft_changed"
	staleReasonDecided     = "already_decided"
	staleReasonEndNotFound = "item_not_found"
)

// suggestionOf reads the project's ready diagnosis and one of its suggestions.
func (s *diagnosisService) suggestionOf(ctx context.Context, token string, projectID string,
	suggestionID string) (*interfaces.Diagnosis, *interfaces.DiagnosisSuggestion, error) {
	diagnosis, err := s.Get(ctx, token, projectID)
	if err != nil {
		return nil, nil, err
	}
	if diagnosis.State != interfaces.DIAGNOSIS_STATE_READY {
		return nil, nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Diagnosis_Running).
			WithErrorDetails(map[string]any{"reason": notApplicableNotReady, "state": diagnosis.State})
	}
	for _, sg := range diagnosis.Suggestions {
		if sg.ID == suggestionID {
			return diagnosis, sg, nil
		}
	}
	return nil, nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Diagnosis_SuggestionNotFound)
}

func suggestionStale(ctx context.Context, reason string) error {
	return httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Diagnosis_SuggestionStale).
		WithErrorDetails(map[string]any{"reason": reason})
}

func notApplicable(ctx context.Context, reason string) error {
	return httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Diagnosis_NotApplicable).
		WithErrorDetails(map[string]any{"reason": reason})
}

func (s *diagnosisService) Apply(ctx context.Context, token string, projectID string, suggestionID string,
	req interfaces.DiagnosisApply, caller interfaces.AccountInfo) (*interfaces.DiagnosisApplied, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ApplySuggestion")
	defer span.End()

	diagnosis, sg, err := s.suggestionOf(ctx, token, projectID, suggestionID)
	if err != nil {
		span.SetStatus(codes.Error, "No suggestion")
		return nil, err
	}
	if sg.Status != interfaces.DIAGNOSIS_STATUS_OPEN {
		span.SetStatus(codes.Error, "Suggestion decided")
		return nil, suggestionStale(ctx, staleReasonDecided)
	}
	switch sg.Kind {
	case interfaces.DIAGNOSIS_KIND_CHOOSE_IDENTITY:
		return nil, notApplicable(ctx, notApplicableChooser)
	case interfaces.DIAGNOSIS_KIND_DATA_QUALITY:
		return nil, notApplicable(ctx, notApplicableNoApply)
	case interfaces.DIAGNOSIS_KIND_WRITEBACK_OBJECT:
		if projecthooks.Current().WritebackObjects == nil {
			return nil, notApplicable(ctx, notApplicableEdition)
		}
	}
	if req.DraftUpdateTime <= 0 && sg.Kind != interfaces.DIAGNOSIS_KIND_CONCEPT_DISPOSITION {
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_MissingUpdateTime)
	}
	current, err := s.drs.GetDraft(ctx, token, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get draft failed")
		return nil, err
	}
	if sg.Kind != interfaces.DIAGNOSIS_KIND_CONCEPT_DISPOSITION && current.UpdateTime != req.DraftUpdateTime {
		span.SetStatus(codes.Error, "Draft stale")
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_Stale)
	}
	if reason := stillHolds(sg, &current.Items); reason != "" {
		span.SetStatus(codes.Error, "Suggestion no longer holds")
		return nil, suggestionStale(ctx, reason)
	}
	updated, err := s.execute(ctx, token, projectID, sg, req.DraftUpdateTime, caller)
	if err != nil {
		span.SetStatus(codes.Error, "Apply failed")
		return nil, err
	}
	if updated == nil {
		updated = current
	}
	sg.Status = interfaces.DIAGNOSIS_STATUS_APPLIED
	s.keepCurrent(ctx, diagnosis, current, updated, sg)
	span.SetStatus(codes.Ok, "")
	return &interfaces.DiagnosisApplied{DraftUpdateTime: updated.UpdateTime, Suggestion: sg}, nil
}

// stillHolds is why a suggestion no longer holds on the draft as it is, empty when it does.
func stillHolds(sg *interfaces.DiagnosisSuggestion, items *interfaces.DraftItems) string {
	switch sg.Kind {
	case interfaces.DIAGNOSIS_KIND_ADOPT_RELATION:
		var a interfaces.DiagnosisAdoptRelation
		_ = json.Unmarshal(sg.Apply, &a)
		rt := projecthooks.FindRelationType(items, a.RelationTypeID)
		switch {
		case rt == nil:
			return staleReasonEndNotFound
		case rt.Adopted || !projecthooks.ObjectTypeAdopted(items, rt.Source) || !projecthooks.ObjectTypeAdopted(items, rt.Target):
			return staleReasonChanged
		}
	case interfaces.DIAGNOSIS_KIND_ADOPT_OBJECT, interfaces.DIAGNOSIS_KIND_EXCLUDE_OBJECT:
		var a interfaces.DiagnosisObject
		_ = json.Unmarshal(sg.Apply, &a)
		ot := projecthooks.FindObjectType(items, a.ObjectTypeID)
		switch {
		case ot == nil:
			return staleReasonEndNotFound
		case sg.Kind == interfaces.DIAGNOSIS_KIND_ADOPT_OBJECT && (ot.Adopted || ot.NeedsIdentity),
			sg.Kind == interfaces.DIAGNOSIS_KIND_EXCLUDE_OBJECT && !ot.Adopted:
			return staleReasonChanged
		}
	case interfaces.DIAGNOSIS_KIND_CREATE_RELATION:
		var a interfaces.DiagnosisCreateRelation
		_ = json.Unmarshal(sg.Apply, &a)
		source, target := projecthooks.FindObjectType(items, a.Source), projecthooks.FindObjectType(items, a.Target)
		if source == nil || target == nil || len(a.Mapping) != 1 ||
			projecthooks.FindProperty(source, a.Mapping[0].SourceProperty) == nil ||
			projecthooks.FindProperty(target, a.Mapping[0].TargetProperty) == nil {
			return staleReasonEndNotFound
		}
		if slices.ContainsFunc(items.RelationTypes, func(r *interfaces.DraftRelationType) bool {
			return r.LinkTable == nil && r.Source == a.Source && r.Target == a.Target && slices.Equal(r.Mapping, a.Mapping)
		}) {
			return staleReasonChanged
		}
	case interfaces.DIAGNOSIS_KIND_WRITEBACK_OBJECT:
		var a interfaces.DiagnosisWriteback
		_ = json.Unmarshal(sg.Apply, &a)
		if ot := projecthooks.FindObjectType(items, a.TrackedObjectTypeID); ot == nil || !ot.Adopted {
			return staleReasonChanged
		}
	}
	return ""
}

// execute runs the suggestion's operation; the draft it returns is the draft as it became, nil when
// the operation did not change the draft.
func (s *diagnosisService) execute(ctx context.Context, token string, projectID string, sg *interfaces.DiagnosisSuggestion,
	updateTime int64, caller interfaces.AccountInfo) (*interfaces.Draft, error) {
	switch sg.Kind {
	case interfaces.DIAGNOSIS_KIND_ADOPT_RELATION:
		var a interfaces.DiagnosisAdoptRelation
		_ = json.Unmarshal(sg.Apply, &a)
		adopt := true
		return s.drs.EditDraftItem(ctx, token, projectID, interfaces.DRAFT_KIND_RELATION_TYPES, a.RelationTypeID,
			interfaces.DraftItemEdit{UpdateTime: updateTime, Adopted: &adopt}, caller)
	case interfaces.DIAGNOSIS_KIND_ADOPT_OBJECT, interfaces.DIAGNOSIS_KIND_EXCLUDE_OBJECT:
		var a interfaces.DiagnosisObject
		_ = json.Unmarshal(sg.Apply, &a)
		adopt := sg.Kind == interfaces.DIAGNOSIS_KIND_ADOPT_OBJECT
		edit := interfaces.DraftItemEdit{UpdateTime: updateTime, Adopted: &adopt}
		if !adopt {
			edit.Reason = truncate(sg.Reason, interfaces.DRAFT_REASON_MAX_LENGTH)
		}
		return s.drs.EditDraftItem(ctx, token, projectID, interfaces.DRAFT_KIND_OBJECT_TYPES, a.ObjectTypeID, edit, caller)
	case interfaces.DIAGNOSIS_KIND_CREATE_RELATION:
		var a interfaces.DiagnosisCreateRelation
		_ = json.Unmarshal(sg.Apply, &a)
		rules := []interfaces.DraftRelationMapping{}
		for _, m := range a.Mapping {
			rules = append(rules, interfaces.DraftRelationMapping{SourceProperty: interfaces.DraftMappedField{Name: m.SourceProperty},
				TargetProperty: interfaces.DraftMappedField{Name: m.TargetProperty}})
		}
		mapping, _ := json.Marshal(rules)
		return s.drs.CreateRelationType(ctx, token, projectID, interfaces.DraftRelationTypeSave{UpdateTime: updateTime,
			Name: a.Name, Comment: a.Description, SourceObjectTypeID: a.Source, TargetObjectTypeID: a.Target,
			Type: interfaces.DRAFT_RELATION_DIRECT, MappingRules: mapping, Cardinality: a.Cardinality}, caller)
	case interfaces.DIAGNOSIS_KIND_WRITEBACK_OBJECT:
		var spec projecthooks.WritebackSpec
		_ = json.Unmarshal(sg.Apply, &spec)
		if _, err := projecthooks.Current().WritebackObjects.Create(ctx, token, projectID, spec, caller); err != nil {
			return nil, err
		}
		return s.drs.GetDraft(ctx, token, projectID)
	case interfaces.DIAGNOSIS_KIND_CONCEPT_DISPOSITION:
		var a interfaces.DiagnosisConceptDisposition
		_ = json.Unmarshal(sg.Apply, &a)
		req, exist, err := s.rqa.GetRequirement(ctx, projectID)
		if err != nil {
			return nil, internalError(ctx, "Get requirement failed", err)
		}
		if !exist || !slices.ContainsFunc(req.Spec.Concepts, func(c *interfaces.RequirementConcept) bool {
			return c.ID == a.ConceptID
		}) {
			return nil, suggestionStale(ctx, staleReasonEndNotFound)
		}
		category := a.Disposition
		if _, err := s.rqs.EditItem(ctx, token, projectID, interfaces.REQUIREMENT_KIND_CONCEPTS, a.ConceptID,
			interfaces.RequirementItemEdit{UpdateTime: req.UpdateTime, Category: &category}, caller); err != nil {
			return nil, err
		}
		return nil, nil
	}
	return nil, notApplicable(ctx, notApplicableNoApply)
}

// keepCurrent records a suggestion's new status and, when the diagnosis held for the draft as it was
// before this change, moves it on to the draft as it became, with its components and connectivity
// recomputed and a reference the change covers counted as covered. Reachability stays until the next
// run. Recording is best effort: the draft change stands either way.
func (s *diagnosisService) keepCurrent(ctx context.Context, diagnosis *interfaces.Diagnosis, before *interfaces.Draft,
	after *interfaces.Draft, sg *interfaces.DiagnosisSuggestion) {
	if diagnosis.DraftUpdateTime == before.UpdateTime && after.UpdateTime != before.UpdateTime {
		diagnosis.DraftUpdateTime = after.UpdateTime
		diagnosis.Components = componentsOf(&after.Items)
		diagnosis.Metrics.Connectivity = connectivityOf(diagnosis.Components)
		if covers(sg) && diagnosis.Metrics.References.Covered < diagnosis.Metrics.References.Total {
			diagnosis.Metrics.References.Covered++
		}
	}
	diagnosis.Stale = false
	if saved, err := s.dga.SaveDiagnosis(ctx, diagnosis); err != nil || !saved {
		otellog.LogWarn(ctx, "Record suggestion status: the diagnosis was not saved")
	}
}

// covers tells a suggestion that makes, adopts or dismisses a relation for a reference at the threshold.
func covers(sg *interfaces.DiagnosisSuggestion) bool {
	return (sg.Kind == interfaces.DIAGNOSIS_KIND_CREATE_RELATION || sg.Kind == interfaces.DIAGNOSIS_KIND_ADOPT_RELATION) &&
		sg.Evidence.Rows > 0 && !sg.Evidence.NotCounted
}

func (s *diagnosisService) Dismiss(ctx context.Context, token string, projectID string, suggestionID string,
	req interfaces.DiagnosisDismiss, caller interfaces.AccountInfo) (*interfaces.DiagnosisSuggestion, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DismissSuggestion")
	defer span.End()

	diagnosis, sg, err := s.suggestionOf(ctx, token, projectID, suggestionID)
	if err != nil {
		span.SetStatus(codes.Error, "No suggestion")
		return nil, err
	}
	if sg.Status == interfaces.DIAGNOSIS_STATUS_DISMISSED {
		span.SetStatus(codes.Ok, "")
		return sg, nil
	}
	if sg.Status != interfaces.DIAGNOSIS_STATUS_OPEN {
		span.SetStatus(codes.Error, "Suggestion decided")
		return nil, suggestionStale(ctx, staleReasonDecided)
	}
	before, err := s.drs.GetDraft(ctx, token, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get draft failed")
		return nil, err
	}
	after, err := s.drs.DismissSuggestion(ctx, token, projectID, suggestionID, req.Reason, caller)
	if err != nil {
		span.SetStatus(codes.Error, "Record dismissal failed")
		return nil, err
	}
	sg.Status = interfaces.DIAGNOSIS_STATUS_DISMISSED
	s.keepCurrent(ctx, diagnosis, before, after, sg)
	span.SetStatus(codes.Ok, "")
	return sg, nil
}
