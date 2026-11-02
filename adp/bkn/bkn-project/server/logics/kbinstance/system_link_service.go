// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbconcept"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbdataset"
)

type SystemLinkRequest struct {
	OperationID string `json:"operation_id"`
	Signature   string `json:"signature"`
}
type SystemLinkSummary struct {
	RelationID string `json:"relation_id"`
	Name       string `json:"name"`
	Eligible   int64  `json:"eligible"`
	Linked     int64  `json:"linked"`
	Pending    int64  `json:"pending"`
	Both       int    `json:"both"`
	SystemOnly int    `json:"system_only"`
	TextOnly   int    `json:"text_only"`
	Conflicts  *int   `json:"conflicts"`
}
type SystemLinkPreview struct {
	Signature string              `json:"signature"`
	Eligible  int64               `json:"eligible"`
	Linked    int64               `json:"linked"`
	Pending   int64               `json:"pending"`
	Relations []SystemLinkSummary `json:"relations"`
}
type systemLinkDatasets interface {
	kb.DatasetFileSnapshot
	kb.DatasetFileRecords
}
type SystemLinkService struct {
	Builds   *BuildService
	Datasets systemLinkDatasets
	Vega     interfaces.VegaAccess
	Now      func() time.Time
}
type systemLinkScope struct {
	Spec interfaces.DraftDatasetSpec
	File kb.DatasetFileScope
	Keep []string
}
type preparedSystemLinks struct {
	Preview SystemLinkPreview
	Writes  []kb.DatasetWrite
	Scopes  []systemLinkScope
}

// Compare only a reviewed shared-field plan. Similar names alone never grant
// authority to equate a document field with a system column.
func systemComparisonFields(items interfaces.DraftItems, relation *interfaces.DraftRelationType) (*interfaces.DraftObjectType, []string, error) {
	source := instanceObject(items, relation.Source)
	if source == nil || source.Document.DualSource == nil {
		return nil, nil, nil
	}
	if !kbconcept.SourcePlanReady(items, source, true) {
		return nil, nil, ErrBuildChanged
	}
	choice := source.Document.DualSource
	if choice.RelationTypeID != relation.ID || choice.SystemObjectTypeID != relation.Target {
		return nil, nil, nil
	}
	file := instanceObject(items, choice.FileObjectTypeID)
	if file == nil {
		return nil, nil, ErrBuildChanged
	}
	fields := []string{}
	for _, field := range choice.Fields {
		if field.Category == "both" {
			fields = append(fields, field.Name)
		}
	}
	slices.Sort(fields)
	return file, fields, nil
}

func systemFieldConflicts(items interfaces.DraftItems, file *interfaces.DraftObjectType, properties []string, text map[string]ReconcileRecord, system []ReconcileRecord, claims []SourceClaim) (int, error) {
	identity, err := kbconcept.FileIdentityProperty(items, file)
	if err != nil {
		return 0, err
	}
	index := map[string]ReconcileRecord{}
	for _, row := range system {
		key, err := businessJoinKey(row.Identity)
		if err != nil {
			return 0, err
		}
		index[key] = row
	}
	different := map[string]bool{}
	for _, claim := range claims {
		if claim.ObjectTypeID != file.ID || claim.Value == nil || !slices.Contains(properties, claim.Property) {
			continue
		}
		key, err := businessJoinKey(claim.Identity[identity])
		if err != nil {
			return 0, err
		}
		master, matched := index[key]
		if _, inText := text[key]; !matched || !inText {
			continue
		}
		value, exists := master.Values[claim.Property]
		if !exists {
			return 0, ErrBuildChanged
		}
		left, err := canonicalBusinessValue(value)
		if err != nil {
			return 0, err
		}
		right, err := canonicalBusinessValue(claim.Value)
		if err != nil {
			return 0, err
		}
		if string(left) != string(right) {
			different[key+"/"+claim.Property] = true
		}
	}
	return len(different), nil
}

func (s *SystemLinkService) now() int64 {
	if s.Now != nil {
		return s.Now().UnixMilli()
	}
	return time.Now().UnixMilli()
}

// current uses only the live operator. The extraction job's KB-only identity
// cannot borrow this user's table access or store a token for a later worker.
func (s *SystemLinkService) current(ctx context.Context, token, project, id string, caller interfaces.AccountInfo) (*interfaces.KBBuildRecord, *interfaces.Draft, error) {
	if s.Builds == nil || s.Builds.Store == nil || s.Builds.Drafts == nil || s.Builds.Selections == nil || s.Datasets == nil || s.Vega == nil {
		return nil, nil, kb.ErrUnavailable
	}
	entry, err := s.Builds.owner(ctx, token, project, caller)
	if err != nil {
		return nil, nil, err
	}
	if entry.Archived {
		return nil, nil, ErrBuildChanged
	}
	record, err := s.Builds.Store.Get(ctx, project, id)
	if err != nil {
		return nil, nil, err
	}
	if record == nil || record.Build == nil {
		return nil, nil, &kb.SourceError{StatusCode: 404}
	}
	build := record.Build
	if build.ID != id || build.ProjectID != project || build.Owner != caller || build.NetworkID != entry.KNID || !record.Identity.Cleaned || (build.State != "succeeded" && build.State != "linking" && build.State != "linking_attention") {
		return nil, nil, ErrBuildChanged
	}
	state, err := s.Builds.Selections.Read(ctx, project)
	if err != nil {
		return nil, nil, err
	}
	if state == nil || state.Current == nil || state.Current.State != "active" || state.Current.ID != build.SelectionID || state.Current.Revision != build.SelectionRevision || state.Current.Owner != caller {
		return nil, nil, ErrBuildChanged
	}
	selected := map[string]interfaces.KBResourceFile{}
	for _, file := range state.Current.Files {
		selected[file.Key()] = file
	}
	files := []interfaces.KBResourceFile{}
	for _, step := range build.Files {
		if step.State != "succeeded" || selected[step.File.Key()] != step.File {
			return nil, nil, ErrBuildChanged
		}
		files = append(files, step.File)
	}
	draft, found, err := s.Builds.Drafts.GetDraft(ctx, project)
	if err != nil {
		return nil, nil, err
	}
	if !found || draft == nil || draft.Status != interfaces.DRAFT_STATUS_READY || draft.UpdateTime != build.DraftUpdateTime || draft.Items.Confirmation == nil || draft.Items.Confirmation.UpdateTime != draft.UpdateTime || draft.Items.Confirmation.Commit != build.DefinitionCommit {
		return nil, nil, ErrBuildChanged
	}
	fingerprint, err := kbdataset.ModelFingerprint(draft)
	if err != nil || fingerprint != build.ModelFingerprint {
		return nil, nil, ErrBuildChanged
	}
	if err := s.Builds.sourcePermission(ctx, token, files); err != nil {
		return nil, nil, err
	}
	return record, draft, nil
}

func (s *SystemLinkService) prepare(ctx context.Context, record *interfaces.KBBuildRecord, draft *interfaces.Draft, guard func(context.Context) error) (*preparedSystemLinks, error) {
	build := record.Build
	claims, err := ReadSourceClaims(ctx, s.Datasets, build, draft, guard)
	if err != nil {
		return nil, err
	}
	result := &preparedSystemLinks{Preview: SystemLinkPreview{Relations: []SystemLinkSummary{}}, Writes: []kb.DatasetWrite{}, Scopes: []systemLinkScope{}}
	digests := map[string]string{}
	for _, relation := range draft.Items.RelationTypes {
		if relation == nil || !relation.Adopted || relation.Document == nil || relation.DocumentMapping == nil || relation.DocumentMapping.Mode != "linking" {
			continue
		}
		var target *interfaces.DraftObjectType
		for _, object := range draft.Items.ObjectTypes {
			if object != nil && object.ID == relation.Target {
				target = object
			}
		}
		// This operation is specifically the structured-source continuation.
		// Document-to-document linking and shared-term decisions have their own
		// inputs; completing these table links cannot certify those other steps.
		if target == nil || target.Document != nil {
			continue
		}
		comparisonFile, properties, err := systemComparisonFields(draft.Items, relation)
		if err != nil {
			return nil, err
		}
		snapshot, err := ReadSystemSnapshot(ctx, s.Vega, build.Owner, target, relation.DocumentMapping.IdentityProperty, properties, guard)
		if err != nil {
			return nil, err
		}
		plan, err := PlanSystemLinks(build, draft, relation, claims, snapshot, nil)
		if err != nil {
			return nil, err
		}
		digests[relation.ID] = plan.SystemDigest
		result.Writes = append(result.Writes, plan.Writes...)
		text := map[string]ReconcileRecord{}
		for _, claim := range claims {
			if claim.ObjectTypeID != relation.Source || claim.Property != relation.DocumentMapping.MentionProperty {
				continue
			}
			key, err := businessJoinKey(claim.Value)
			if err != nil {
				return nil, err
			}
			text[key] = ReconcileRecord{Identity: claim.Value}
		}
		textRows := []ReconcileRecord{}
		for _, row := range text {
			textRows = append(textRows, row)
		}
		reconciled, err := ReconcileSources(snapshot.Records, textRows, nil)
		if err != nil {
			return nil, err
		}
		summary := SystemLinkSummary{RelationID: relation.ID, Name: relation.Name, Eligible: int64(plan.Resolution.Eligible), Linked: int64(len(plan.Resolution.Linked)), Pending: int64(len(plan.Resolution.Pending)), Both: reconciled.Both, SystemOnly: reconciled.SystemOnly, TextOnly: reconciled.TextOnly}
		if len(properties) > 0 {
			count, err := systemFieldConflicts(draft.Items, comparisonFile, properties, text, snapshot.Records, claims)
			if err != nil {
				return nil, err
			}
			summary.Conflicts = &count
		}
		result.Preview.Relations = append(result.Preview.Relations, summary)
		result.Preview.Eligible += summary.Eligible
		result.Preview.Linked += summary.Linked
		result.Preview.Pending += summary.Pending
		for index, step := range build.Files {
			rows, err := newFileRows(build, index, draft, "")
			if err != nil {
				return nil, err
			}
			spec, exists := rows.relations[relation.ID]
			if !exists {
				return nil, ErrBuildChanged
			}
			scope := systemLinkScope{Spec: spec, File: kb.DatasetFileScope{KnowledgeBaseID: step.File.KnowledgeBaseID, FileID: step.File.FileID, ContentVersionID: step.File.ContentVersionID, DocumentID: rows.document, ModelFingerprint: build.ModelFingerprint}, Keep: []string{}}
			for _, write := range plan.Writes {
				if write.Row["document_id"] == rows.document {
					scope.Keep = append(scope.Keep, write.Row["_id"].(string))
				}
			}
			slices.Sort(scope.Keep)
			result.Scopes = append(result.Scopes, scope)
		}
	}
	if _, err := validateWrites(build, draft, result.Writes); err != nil {
		return nil, err
	}
	slices.SortFunc(result.Preview.Relations, func(a, b SystemLinkSummary) int { return strings.Compare(a.RelationID, b.RelationID) })
	slices.SortFunc(result.Scopes, func(a, b systemLinkScope) int {
		return strings.Compare(a.Spec.ResourceID+"/"+a.File.DocumentID, b.Spec.ResourceID+"/"+b.File.DocumentID)
	})
	raw, err := json.Marshal(struct {
		Model   string
		Claims  []SourceClaim
		Targets map[string]string
		Writes  []kb.DatasetWrite
		Scopes  []systemLinkScope
	}{build.ModelFingerprint, claims, digests, result.Writes, result.Scopes})
	if err != nil || len(raw) > 32<<20 {
		return nil, ErrInvalidBuild
	}
	hash := sha256.Sum256(raw)
	result.Preview.Signature = hex.EncodeToString(hash[:])
	if err := guard(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *SystemLinkService) Preview(ctx context.Context, token, project, id string, caller interfaces.AccountInfo) (*SystemLinkPreview, error) {
	record, draft, err := s.current(ctx, token, project, id, caller)
	if err != nil {
		return nil, err
	}
	guard := func(ctx context.Context) error { _, _, err := s.current(ctx, token, project, id, caller); return err }
	prepared, err := s.prepare(ctx, record, draft, guard)
	if err != nil {
		return nil, err
	}
	return &prepared.Preview, nil
}

// Apply journals only the operation and acknowledgement counts. All rows are
// validated before the first write, old links are pruned only after all writes
// and a fresh complete snapshot check, and replay uses identical row identities.
func (s *SystemLinkService) Apply(ctx context.Context, token, project, id string, caller interfaces.AccountInfo, req SystemLinkRequest) (*interfaces.KBLinkingReceipt, error) {
	operation, err := uuid.Parse(req.OperationID)
	signature, signatureErr := hex.DecodeString(req.Signature)
	if err != nil || operation.String() != req.OperationID || signatureErr != nil || len(signature) != 32 {
		return nil, &kb.SourceError{StatusCode: 400}
	}
	if s.Builds == nil {
		return nil, kb.ErrUnavailable
	}
	store, available := s.Builds.Store.(interfaces.KBLinkingAccess)
	if !available {
		return nil, kb.ErrUnavailable
	}
	record, draft, err := s.current(ctx, token, project, id, caller)
	if err != nil {
		return nil, err
	}
	readGuard := func(ctx context.Context) error { _, _, err := s.current(ctx, token, project, id, caller); return err }
	prepared, err := s.prepare(ctx, record, draft, readGuard)
	if err != nil {
		return nil, err
	}
	if prepared.Preview.Signature != req.Signature {
		return nil, ErrBuildChanged
	}
	now := s.now()
	record, err = store.ClaimLinking(ctx, project, id, caller, req.OperationID, req.Signature, now, now+900000)
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, ErrBuildChanged
	}
	if record.Build.Linking != nil && record.Build.Linking.State == "succeeded" {
		return record.Build.Linking, nil
	}
	guard := func(ctx context.Context) error {
		ok, err := store.CheckLinking(ctx, project, id, req.OperationID, record.Build.Revision, s.now())
		if err != nil {
			return err
		}
		if !ok {
			return ErrBuildChanged
		}
		return readGuard(ctx)
	}
	written := int64(0)
	finish := func(state string) error {
		previous := record.Build.Revision
		record.Build.Revision++
		record.Build.UpdateTime = max(s.now(), record.Build.UpdateTime+1)
		record.Build.State = "succeeded"
		if state != "succeeded" {
			record.Build.State = "linking_attention"
		}
		record.Build.Linking = &interfaces.KBLinkingReceipt{OperationID: req.OperationID, Signature: req.Signature, State: state, RowsWritten: written, Eligible: prepared.Preview.Eligible, Linked: prepared.Preview.Linked, Pending: prepared.Preview.Pending}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		ok, err := store.SaveLinking(cleanup, record, previous, s.now())
		if err != nil {
			return err
		}
		if !ok {
			return ErrBuildChanged
		}
		return nil
	}
	fail := func(err error) (*interfaces.KBLinkingReceipt, error) { _ = finish("attention"); return nil, err }
	verify := func() error {
		if err := guard(ctx); err != nil {
			return err
		}
		fresh, err := s.prepare(ctx, record, draft, guard)
		if err != nil {
			return err
		}
		if fresh.Preview.Signature != req.Signature {
			return ErrBuildChanged
		}
		return nil
	}
	if err := verify(); err != nil {
		return fail(err)
	}
	for _, write := range prepared.Writes {
		if err := guard(ctx); err != nil {
			return fail(err)
		}
		if err := s.Datasets.Replace(ctx, write.Spec, write.Row); err != nil {
			return fail(err)
		}
		written++
	}
	if err := verify(); err != nil {
		return fail(err)
	}
	for _, scope := range prepared.Scopes {
		if err := guard(ctx); err != nil {
			return fail(err)
		}
		if err := s.Datasets.Prune(ctx, scope.Spec, scope.File, scope.Keep, guard); err != nil {
			return fail(err)
		}
	}
	if err := verify(); err != nil {
		return fail(err)
	}
	if err := finish("succeeded"); err != nil {
		return nil, err
	}
	return record.Build.Linking, nil
}
