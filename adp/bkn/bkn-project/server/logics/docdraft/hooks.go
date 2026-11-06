// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package docdraft holds what document items add to a draft. It stands for the package that leaves
// the open core: it reaches the draft service only through projecthooks.
package docdraft

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/projecthooks"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbconcept"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbpublication"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
)

// service is the draft service as the hooks reach it, with the source selection of each project.
type service struct {
	projecthooks.Host
	resources interfaces.KBResourceAccess
}

func (hooks Hooks) service(h projecthooks.Host) service { return service{h, hooks.Resources} }

// Hooks implements projecthooks.Drafts. Resources is the source selection of each project.
type Hooks struct{ Resources interfaces.KBResourceAccess }

var _ projecthooks.Drafts = Hooks{}

func (Hooks) CheckRead(ctx context.Context, token string, draft *interfaces.Draft) error {
	return CheckDocumentReadAccess(ctx, token, draft)
}

func (Hooks) ProjectRead(items *interfaces.DraftItems) {
	kbconcept.RefreshFactResolutionStates(*items)
	for _, object := range items.ObjectTypes {
		if object == nil || object.Document == nil || object.Document.DualSource == nil {
			continue
		}
		plan := object.Document.DualSource
		plan.State = "proposed"
		if plan.ConfirmedAt > 0 {
			plan.State = "stale"
			if kbconcept.SourcePlanReady(*items, object, false) {
				plan.State = "confirmed"
			}
		}
	}
}

func (Hooks) SnapshotObject(ot *interfaces.DraftObjectType, s *interfaces.DraftObjectTypeSnapshot) {
	if ot.Document == nil {
		return
	}
	if ot.Document.DualSource != nil {
		plan := *ot.Document.DualSource
		plan.State = ""
		plan.Fields = slices.Clone(plan.Fields)
		for index := range plan.Fields {
			plan.Fields[index].EvidenceOrder = slices.Clone(plan.Fields[index].EvidenceOrder)
		}
		s.DualSource = &plan
	}
	s.Document = &interfaces.DraftDocumentSettings{TimeProperty: ot.Document.TimeProperty, ParticipantRelations: append([]string(nil), ot.Document.ParticipantRelations...)}
	if proof := ot.Document.SentenceIdentity; proof != nil {
		s.Document.SentenceIdentity = &interfaces.SentenceIdentityDecision{Properties: slices.Clone(proof.Properties), Confirm: proof.ConfirmedAt > 0 && proof.ConfirmedBy.ID != ""}
	}
	if proof := ot.Document.FactResolution; proof != nil {
		s.Document.FactResolution = &interfaces.FactResolutionDecision{ValueProperty: proof.ValueProperty, SubjectRelationID: proof.SubjectRelationID, Confirm: proof.ConfirmedBy.ID != "" && proof.ConfirmedAt > 0}
	}
	if proof := ot.Document.FilePeriod; proof != nil {
		s.Document.FilePeriod = &interfaces.FilePeriodDecision{From: proof.From, Until: proof.Until, Confirm: proof.ConfirmedAt > 0 && proof.ConfirmedBy.ID != ""}
	}
	if proof := ot.Document.FileIdentity; proof != nil {
		s.Document.FileIdentity = &interfaces.FileIdentityDecision{Property: proof.Property, Confirm: proof.ConfirmedAt > 0 && proof.ConfirmedBy.ID != ""}
	}
}

func (Hooks) SnapshotRelation(rt *interfaces.DraftRelationType, s *interfaces.DraftRelationTypeSnapshot) {
	s.DocumentRepair = rt.DocumentRepair
	if rt.Document != nil {
		s.DocumentMapping = rt.DocumentMapping
		s.DocumentCardinality = rt.Cardinality
	}
}

func (hooks Hooks) SaveObject(ctx context.Context, h projecthooks.Host, token string, draft *interfaces.Draft, ot *interfaces.DraftObjectType, save interfaces.DraftObjectTypeSave, caller interfaces.AccountInfo) (*interfaces.Draft, bool, error) {
	if ot.Document == nil && save.Document != nil && save.Document.Derive != nil {
		return nil, true, documentEditError(ctx)
	}
	if ot.Document == nil {
		return nil, false, nil
	}
	saved, err := hooks.service(h).saveDocumentObject(ctx, token, draft, ot, save, caller)
	return saved, true, err
}

func (hooks Hooks) CreateRelation(ctx context.Context, h projecthooks.Host, token string, draft *interfaces.Draft, rt *interfaces.DraftRelationType, save interfaces.DraftRelationTypeSave, caller interfaces.AccountInfo) (*interfaces.Draft, bool, error) {
	if save.DocumentMapping == nil {
		return nil, false, nil
	}
	saved, err := hooks.service(h).createDocumentRelation(ctx, token, draft, rt, save, caller)
	return saved, true, err
}

func (hooks Hooks) SaveRelation(ctx context.Context, h projecthooks.Host, token string, draft *interfaces.Draft, rt *interfaces.DraftRelationType, save interfaces.DraftRelationTypeSave, caller interfaces.AccountInfo) (*interfaces.Draft, bool, error) {
	if rt.Document != nil {
		saved, err := hooks.service(h).saveDocumentRelation(ctx, token, draft, rt, save, caller)
		return saved, true, err
	}
	if save.DocumentMapping != nil {
		return nil, true, h.InvalidRelation(ctx, map[string]any{"reason": projecthooks.RelationNoMapping})
	}
	return nil, false, nil
}

func (Hooks) CheckRelationDelete(ctx context.Context, items *interfaces.DraftItems, relationID string) error {
	for _, object := range items.ObjectTypes {
		if object != nil && object.Document != nil && object.Document.Role == "event" && slices.Contains(object.Document.ParticipantRelations, relationID) {
			return httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_EventParticipantInUse)
		}
	}
	return nil
}

func (hooks Hooks) CommitEdit(ctx context.Context, h projecthooks.Host, draft *interfaces.Draft, decisions []*interfaces.DraftDecision, caller interfaces.AccountInfo, model *interfaces.DraftDocumentModel) (bool, error) {
	if model == nil {
		return false, nil
	}
	return true, hooks.service(h).commitDocumentEdit(ctx, draft, decisions, caller, model)
}

func (Hooks) CheckIdentity(ctx context.Context, items *interfaces.DraftItems, objectType *interfaces.DraftObjectType, properties []string) error {
	if objectType.Document == nil {
		return nil
	}
	invalid := httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidIdentity)
	if len(properties) > 5 {
		return invalid
	}
	for _, relation := range items.RelationTypes {
		if relation != nil && relation.Target == objectType.ID && relation.DocumentMapping != nil && relation.DocumentMapping.Mode == "linking" && !slices.Equal(properties, []string{relation.DocumentMapping.IdentityProperty}) {
			return invalid
		}
	}
	if objectType.Document.Role != "entity" && !slices.Equal(properties, objectType.PrimaryKeys) {
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidIdentity)
	}
	if objectType.Document.Role == "entity" {
		for _, name := range properties {
			if p := projecthooks.FindProperty(objectType, name); p != nil && p.ReadOnly {
				return httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidIdentity)
			}
		}
	}
	return nil
}

func (Hooks) SetIdentity(objectType *interfaces.DraftObjectType, names []string) ([]string, bool) {
	if objectType.Document == nil {
		return nil, false
	}
	before := append([]string(nil), objectType.PrimaryKeys...)
	objectType.PrimaryKeys = names
	objectType.Key = interfaces.DraftKeyEvidence{Source: interfaces.DRAFT_KEY_CHOSEN}
	objectType.NeedsIdentity = false
	if objectType.DisplayKey == "" {
		objectType.DisplayKey = names[0]
	}
	return before, true
}

func (hooks Hooks) AskObject(ctx context.Context, h projecthooks.Host, token string, draft *interfaces.Draft, original, working *interfaces.DraftObjectType) (projecthooks.AskObject, error) {
	checked, examples, err := hooks.service(h).documentAskObject(ctx, token, draft, original, working)
	if err != nil {
		return projecthooks.AskObject{}, err
	}
	return projecthooks.AskObject{Working: checked, Examples: examples, Editable: askObjectEditable(checked)}, nil
}

// askDocument is what the Agent reads of a document item.
type askDocument struct {
	BusinessNumber       string   `json:"business_number,omitempty"`
	SentenceBusinessKey  []string `json:"sentence_business_key,omitempty"`
	TimeProperty         string   `json:"time_property,omitempty"`
	ParticipantRelations []string `json:"participant_relations,omitempty"`
	Role                 string   `json:"role"`
	Layer                string   `json:"layer"`
	Reason               string   `json:"reason"`
}

// AskAgentID keeps the Agent of the open core, whose prompt still covers document items.
func (Hooks) AskAgentID(*interfaces.Draft) string { return "" }

func (Hooks) AskObjectContext(objectType *interfaces.DraftObjectType) (any, map[string]string) {
	if objectType.Document == nil {
		return nil, nil
	}
	asked := &askDocument{Role: objectType.Document.Role, Layer: objectType.Document.Layer, Reason: objectType.Document.Reason, TimeProperty: objectType.Document.TimeProperty, ParticipantRelations: append([]string(nil), objectType.Document.ParticipantRelations...)}
	if objectType.Document.FileIdentity != nil {
		asked.BusinessNumber = objectType.Document.FileIdentity.Property
	}
	if objectType.Document.SentenceIdentity != nil {
		asked.SentenceBusinessKey = append([]string(nil), objectType.Document.SentenceIdentity.Properties...)
	}
	instructions := map[string]string{}
	for _, p := range objectType.Properties {
		if p.Extraction != nil {
			instructions[p.Name] = p.Extraction.Instruction
		}
	}
	return asked, instructions
}

func (Hooks) AskRelationContext(r *interfaces.DraftRelationType) (any, *interfaces.DraftDocumentMapping) {
	if r.Document == nil {
		return nil, nil
	}
	var mapping *interfaces.DraftDocumentMapping
	if r.DocumentMapping != nil {
		copied := *r.DocumentMapping
		copied.BusinessIdentity = nil
		mapping = &copied
	}
	return &askDocument{Role: r.Document.Role, Layer: r.Document.Layer, Reason: r.Document.Reason}, mapping
}

func askObjectEditable(base *interfaces.DraftObjectType) []string {
	if base.Document == nil || base.Document.Role == "entity" {
		return nil
	}
	editable := []string{interfaces.DRAFT_ASK_REVISION_NAME, interfaces.DRAFT_ASK_REVISION_COMMENT}
	if !strings.HasPrefix(base.Document.Role, "evidence_") {
		editable = append(editable, interfaces.DRAFT_ASK_REVISION_DISPLAY_KEY, interfaces.DRAFT_ASK_REVISION_DATA_PROPERTY)
	}
	return editable
}

func askRelationEditable(base *interfaces.DraftRelationType) []string {
	if base.Document != nil && base.Document.Role != "relation" {
		return []string{interfaces.DRAFT_ASK_REVISION_NAME, interfaces.DRAFT_ASK_REVISION_COMMENT}
	}
	return nil
}

func (hooks Hooks) AskRelation(ctx context.Context, h projecthooks.Host, token string, draft *interfaces.Draft, relation, base *interfaces.DraftRelationType) (projecthooks.AskRelation, error) {
	if base.Document == nil {
		return projecthooks.AskRelation{Working: base}, nil
	}
	var input *kbconcept.SampleInput
	if base.Document.Role == "relation" {
		var err error
		input, err = hooks.service(h).documentEditInput(ctx, token, draft, base.Document)
		if err != nil {
			return projecthooks.AskRelation{}, err
		}
		validate := kbconcept.ValidateEditedRelation
		if relation.DocumentRepair != "" {
			validate = kbconcept.ReviewRelationForAdvice
		}
		base, err = validate(input, draft.Items, relation, base)
		if err != nil {
			return projecthooks.AskRelation{}, documentEditError(ctx)
		}
	}
	return projecthooks.AskRelation{Working: base, Editable: askRelationEditable(base), Revise: func(r projecthooks.AskRevisionOutput) (*interfaces.DraftAskRevision, string) {
		return RelationRevision(ctx, h, input, draft.Items, base, r)
	}}, nil
}

func (hooks Hooks) AskNewRelation(ctx context.Context, h projecthooks.Host, token string, draft *interfaces.Draft, req interfaces.DraftRelationAskRequest) (projecthooks.AskRelation, error) {
	if req.Working == nil || req.Working.DocumentMapping == nil {
		return projecthooks.AskRelation{}, nil
	}
	base, reviser, err := hooks.service(h).newDocumentRelation(ctx, token, draft, req)
	if err != nil {
		return projecthooks.AskRelation{}, err
	}
	return projecthooks.AskRelation{Working: base, Revise: reviser}, nil
}

func (Hooks) AskOfferable(object *interfaces.DraftObjectType) bool {
	return object.Document == nil || !strings.HasPrefix(object.Document.Role, "evidence_")
}

func (Hooks) RevisionSchema(limit int) map[string]any {
	str := func(max int) map[string]any { return map[string]any{"type": "string", "maxLength": max} }
	return map[string]any{
		"document_mapping": map[string]any{"type": "object", "properties": map[string]any{
			"mode":             map[string]any{"type": "string", "enum": []string{"linking", "same_document"}},
			"mention_property": str(40), "identity_property": str(40), "source_name_property": str(40), "target_name_property": str(40),
		}, "required": []string{"mode"}, "additionalProperties": false},
	}
}

func (Hooks) WorkingObject(base *interfaces.DraftObjectType, working *interfaces.DraftObjectTypeSave) {
	if base.Document == nil {
		return
	}
	document := *base.Document
	document.Citations = append([]interfaces.DraftSourceCitation(nil), document.Citations...)
	document.ParticipantRelations = append([]string(nil), document.ParticipantRelations...)
	if working.Document != nil {
		document.TimeProperty = working.Document.TimeProperty
		document.ParticipantRelations = append([]string(nil), working.Document.ParticipantRelations...)
	}
	base.Document = &document
	base.DisplayKey = strings.TrimSpace(working.DisplayKey)
}

func (Hooks) WorkingProperty(base *interfaces.DraftObjectType, p *interfaces.DraftProperty, saved interfaces.DraftDataPropertySave) {
	if base.Document == nil || p.ReadOnly {
		return
	}
	if saved.Type != "" {
		p.Type = saved.Type
	}
	if saved.Extraction != nil {
		copied := *saved.Extraction
		copied.Examples = append([]interfaces.DraftSourceCitation(nil), saved.Extraction.Examples...)
		p.Extraction = &copied
	}
}

func (Hooks) WorkingRelation(base *interfaces.DraftRelationType, working *interfaces.DraftRelationTypeSave) bool {
	if base.Document == nil {
		return false
	}
	if working.SourceObjectTypeID != "" {
		base.Source = working.SourceObjectTypeID
	}
	if working.TargetObjectTypeID != "" {
		base.Target = working.TargetObjectTypeID
	}
	if working.Cardinality != "" {
		base.Cardinality = working.Cardinality
	}
	if working.DocumentMapping != nil {
		mapping := *working.DocumentMapping
		base.DocumentMapping = &mapping
	}
	return true
}

func (Hooks) IdentityRevisionAllowed(base *interfaces.DraftObjectType, keys []string, relations []*interfaces.DraftRelationType) bool {
	if base.Document == nil {
		return true
	}
	if base.Document.Role != "entity" || len(keys) > 5 {
		return false
	}
	for _, name := range keys {
		if p := projecthooks.FindProperty(base, name); p != nil && p.ReadOnly {
			return false
		}
	}
	for _, r := range relations {
		if r != nil && r.Target == base.ID && r.DocumentMapping != nil && r.DocumentMapping.Mode == "linking" && !slices.Equal(keys, []string{r.DocumentMapping.IdentityProperty}) {
			return false
		}
	}
	return true
}

func (Hooks) InstructionRevision(base *interfaces.DraftObjectType, p *interfaces.DraftProperty, proposed *string) (*string, string) {
	if base.Document == nil || p.Extraction == nil {
		return nil, projecthooks.AskDroppedNotEditable
	}
	value := strings.TrimSpace(*proposed)
	if value == "" || utf8.RuneCountInString(value) > 1000 {
		return nil, projecthooks.AskDroppedTooLong
	}
	if value != p.Extraction.Instruction {
		return &value, ""
	}
	return nil, ""
}

func (hooks Hooks) CheckConfirm(ctx context.Context, draft *interfaces.Draft) error {
	for _, object := range draft.Items.ObjectTypes {
		if err := confirmObject(ctx, &draft.Items, object); err != nil {
			return err
		}
	}
	for _, relation := range draft.Items.RelationTypes {
		if relation == nil {
			continue
		}
		if err := confirmRelation(ctx, &draft.Items, relation); err != nil {
			return err
		}
	}
	return confirmReady(ctx, draft)
}

func (hooks Hooks) PrepareConfirm(ctx context.Context, h projecthooks.Host, token string, entry *interfaces.ProjectEntry, draft *interfaces.Draft, caller interfaces.AccountInfo) (*interfaces.Draft, error) {
	if err := checkSourceFieldDecisions(ctx, draft); err != nil {
		return nil, err
	}
	s := hooks.service(h)
	if err := s.checkBusinessIdentities(ctx, draft, caller); err != nil {
		return nil, err
	}
	draft, err := s.prepareDocumentDatasets(ctx, token, entry, draft, caller)
	if err != nil {
		return nil, err
	}
	if err := s.checkBusinessIdentities(ctx, draft, caller); err != nil {
		return nil, err
	}
	return draft, nil
}

func confirmObject(ctx context.Context, items *interfaces.DraftItems, object *interfaces.DraftObjectType) error {
	if object != nil && object.Adopted && kbconcept.EventParticipantsNeedRepair(*items, object) {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_DocumentRelationNeedsRepair)
	}
	return nil
}

func confirmRelation(ctx context.Context, items *interfaces.DraftItems, relation *interfaces.DraftRelationType) error {
	if relation.Adopted && relation.DocumentRepair != "" {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_DocumentRelationNeedsRepair)
	}
	if relation.Adopted && relation.DocumentMapping != nil && relation.DocumentMapping.Mode == "same_document" && !kbconcept.SameDocumentScopeReady(*items, relation) {
		return documentValidationError(ctx, &kbconcept.RelationScopeError{ID: relation.ID, Name: relation.Name})
	}
	if relation.Adopted && relation.DocumentMapping != nil && relation.DocumentMapping.Mode == "linking" && !kbconcept.LinkIdentityReady(*items, relation) {
		if kbconcept.VerifiedReferenceTarget(*items, relation) != nil {
			return httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_BusinessIdentityNotVerified)
		}
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_NeedsIdentity)
	}
	return nil
}

// VerifyBusinessIdentity counts a relation's business identity now.
func (hooks Hooks) VerifyBusinessIdentity(ctx context.Context, h projecthooks.Host, draft *interfaces.Draft, relation *interfaces.DraftRelationType, caller interfaces.AccountInfo) (*interfaces.DraftBusinessIdentityEvidence, error) {
	return hooks.service(h).verifyBusinessIdentity(ctx, draft, relation, caller)
}

func confirmReady(ctx context.Context, draft *interfaces.Draft) error {
	// Preserve the original explicit refusal when storage is unavailable. Once
	// installed, complete Dataset preparation after all human decisions validate.
	if adoptedDocuments(draft) {
		providers, enabled := kb.Lookup()
		if !enabled || providers.Datasets == nil {
			return datasetNotReady(ctx, http.StatusConflict)
		}
	}
	return nil
}

func (hooks Hooks) DatasetProgress(ctx context.Context, h projecthooks.Host, draft *interfaces.Draft) {
	if len(draft.Items.DatasetPlans) == 0 || hooks.Resources == nil {
		return
	}
	state, readErr := hooks.Resources.Read(ctx, draft.ProjectID)
	if readErr != nil {
		// This is only a progress projection. Source authorization was
		// independently checked above; failures must not hide the draft.
		otellog.LogWarn(ctx, "Read Dataset preparation progress failed")
		draft.DatasetPreparation = nil
		return
	}
	var selection *interfaces.KBSelection
	if state != nil {
		selection = state.Current
	}
	draft.DatasetPreparation = DatasetPreparation(draft, selection)
}

func (Hooks) ObjectNeedsRepair(items *interfaces.DraftItems, ot *interfaces.DraftObjectType) bool {
	return kbconcept.EventParticipantsNeedRepair(*items, ot)
}

func (Hooks) CheckAdoptRelation(ctx context.Context, items *interfaces.DraftItems, r *interfaces.DraftRelationType) error {
	if r.DocumentMapping != nil && r.DocumentMapping.Mode == "linking" && !kbconcept.LinkIdentityReady(*items, r) {
		if kbconcept.VerifiedReferenceTarget(*items, r) != nil {
			return httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_BusinessIdentityNotVerified)
		}
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_NeedsIdentity)
	}
	return nil
}

func (Hooks) ExportItems(items *interfaces.DraftItems, evidence *interfaces.DraftEvidenceExport) (*interfaces.DraftItems, error) {
	return documentExportItems(items, evidence)
}

func (hooks Hooks) ConfirmEvidence(ctx context.Context, h projecthooks.Host, draft *interfaces.Draft) (*interfaces.DraftEvidenceExport, error) {
	return hooks.service(h).evidenceExport(ctx, draft)
}

func (Hooks) DefinitionRules(ctx context.Context, draft *interfaces.Draft, lineageAvailable bool) ([]byte, error) {
	if !adoptedDocuments(draft) {
		return nil, nil
	}
	if !lineageAvailable {
		return nil, datasetNotReady(ctx, http.StatusServiceUnavailable)
	}
	return kbpublication.Definition(draft.Items)
}

// PrepareDocumentDatasets is prepareDocumentDatasets as a test reaches it.
func (hooks Hooks) PrepareDocumentDatasets(ctx context.Context, h projecthooks.Host, token string, entry *interfaces.ProjectEntry, draft *interfaces.Draft, caller interfaces.AccountInfo) (*interfaces.Draft, error) {
	return hooks.service(h).prepareDocumentDatasets(ctx, token, entry, draft, caller)
}

// CheckSourceFieldDecisions is checkSourceFieldDecisions as a test reaches it.
func CheckSourceFieldDecisions(ctx context.Context, draft *interfaces.Draft) error {
	return checkSourceFieldDecisions(ctx, draft)
}

// CheckBusinessIdentities is checkBusinessIdentities as a test reaches it.
func (hooks Hooks) CheckBusinessIdentities(ctx context.Context, h projecthooks.Host, draft *interfaces.Draft, caller interfaces.AccountInfo) error {
	return hooks.service(h).checkBusinessIdentities(ctx, draft, caller)
}
