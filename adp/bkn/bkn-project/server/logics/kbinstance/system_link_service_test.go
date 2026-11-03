// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbconcept"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbdataset"
	"go.uber.org/mock/gomock"
)

type systemLinkQueue struct{ serviceQueue }

func (q *systemLinkQueue) ClaimLinking(_ context.Context, _, _ string, caller interfaces.AccountInfo, operation, signature string, _, until int64) (*interfaces.KBBuildRecord, error) {
	if q.record.Build.Owner != caller {
		return nil, nil
	}
	if q.record.Build.Linking != nil && q.record.Build.State == "succeeded" && q.record.Build.Linking.Signature == signature && q.record.Build.Linking.State == "succeeded" {
		return cloneProcess(q.record), nil
	}
	q.record.Build.State, q.record.LeaseID, q.record.LeaseUntil = "linking", operation, until
	q.record.Build.Revision++
	q.record.Build.Linking = &interfaces.KBLinkingReceipt{OperationID: operation, Signature: signature, State: "running"}
	return cloneProcess(q.record), nil
}

func TestSystemLinkServiceReportsConfirmedSharedFieldDifferencesWithoutOverwritingTheTable(t *testing.T) {
	for _, systemValue := range []string{"25.50", "26.00"} {
		t.Run(systemValue, func(t *testing.T) {
			s, queue, data, _, access, draft := systemLinkServiceFixture(t)
			build := queue.record.Build
			var source, file, target *interfaces.DraftObjectType
			for _, object := range draft.Items.ObjectTypes {
				switch object.ID {
				case "contract_doc":
					source = object
				case "contract_file":
					file = object
				case "system_contract":
					target = object
				}
			}
			if source == nil || file == nil || target == nil {
				t.Fatal("fixture")
			}
			properties := source.Properties[:0]
			for _, property := range source.Properties {
				if property.Name != "amount" {
					properties = append(properties, property)
				}
			}
			source.Properties = properties
			target.Properties = append(target.Properties, &interfaces.DraftProperty{Name: "amount", DisplayName: "Total", Type: "float", MappedField: "amount"})
			choice := interfaces.DualSourceChoice{SystemObjectTypeID: target.ID, FileObjectTypeID: file.ID, RelationTypeID: "same_contract", Fields: []interfaces.SourceFieldRule{{Name: "id", Category: "system_only", SourceOfRecord: "table", DestinationObjectTypeID: target.ID}, {Name: "number", Category: "both", SourceOfRecord: "table", DestinationObjectTypeID: target.ID}, {Name: "amount", Category: "both", SourceOfRecord: "table", DestinationObjectTypeID: target.ID}}}
			signature, err := kbconcept.SourcePlanSignature(draft.Items, source.ID, choice, true)
			if err != nil {
				t.Fatal(err)
			}
			source.Document.DualSource = &interfaces.DraftDualSourcePlan{DualSourceChoice: choice, Signature: signature, ConfirmedBy: build.Owner, ConfirmedAt: 1}
			plan, err := kbdataset.NewPlan(draft, build.GenerationID, "catalog", build.Owner)
			if err != nil {
				t.Fatal(err)
			}
			plan.State = "ready"
			for i := range plan.Datasets {
				plan.Datasets[i].Ready = true
			}
			draft.Items.DatasetPlans = []*interfaces.DraftDatasetPlan{plan}
			bound, err := kbdataset.BindPreview(draft, plan)
			if err != nil {
				t.Fatal(err)
			}
			*draft = *bound
			build.ModelFingerprint = plan.ModelFingerprint
			_, _, input, task := extractionFixture(t, "contract", false)
			extraction, err := parseFileExtraction(task.Output)
			if err != nil {
				t.Fatal(err)
			}
			for i := range extraction.Records {
				if extraction.Records[i].ObjectTypeID == source.ID {
					delete(extraction.Records[i].Values, "amount")
				}
			}
			raw, _ := json.Marshal(extraction)
			task.Output = string(raw)
			writes, err := MaterializeFile(build, 0, draft, input, task)
			if err != nil {
				t.Fatal(err)
			}
			data.rows = map[string][]map[string]any{}
			for _, write := range writes {
				data.rows[write.Spec.ResourceID] = append(data.rows[write.Spec.ResourceID], write.Row)
			}
			access.tables[0].Columns = append(access.tables[0].Columns, interfaces.VegaColumn{Name: "amount", Type: "float"})
			access.rows = []map[string]any{{"bkn_f_0": systemValue, "bkn_f_1": json.Number("18446744073709551615"), "bkn_f_2": "ABC"}}
			preview, err := s.Preview(context.Background(), "Bearer operator-token", "project", "job", build.Owner)
			if err != nil || len(preview.Relations) != 1 || preview.Relations[0].Conflicts == nil {
				t.Fatal("confirmed shared fields were not compared", preview, err)
			}
			want := 0
			if systemValue != "25.50" {
				want = 1
			}
			if *preview.Relations[0].Conflicts != want || preview.Relations[0].Both != 1 || len(data.written) != 0 || access.rows[0]["bkn_f_0"] != systemValue {
				t.Fatal("reconciliation changed source values or reported wrong differences", preview)
			}
		})
	}
}
func (q *systemLinkQueue) CheckLinking(_ context.Context, _, _ string, operation string, revision, now int64) (bool, error) {
	return q.record.Build.State == "linking" && q.record.LeaseID == operation && q.record.Build.Revision == revision && q.record.LeaseUntil > now, nil
}
func (q *systemLinkQueue) SaveLinking(_ context.Context, next *interfaces.KBBuildRecord, revision, _ int64) (bool, error) {
	if q.record.Build.State != "linking" || q.record.Build.Revision != revision {
		return false, nil
	}
	q.record = cloneProcess(next)
	return true, nil
}

type systemLinkRows struct {
	wire         bool
	native       map[string][]map[string]any
	nativePruned int
	claimSnapshot
	written    []kb.DatasetWrite
	pruned     int
	afterWrite func()
	writeErr   error
}

func (s *systemLinkRows) ReadFileRows(ctx context.Context, spec interfaces.DraftDatasetSpec, scope kb.DatasetFileScope, guard func(context.Context) error) ([]map[string]any, error) {
	rows, err := s.claimSnapshot.ReadFileRows(ctx, spec, scope, guard)
	if err != nil || !s.wire {
		return rows, err
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var decoded []map[string]any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}
func (s *systemLinkRows) Replace(_ context.Context, spec interfaces.DraftDatasetSpec, row map[string]any) error {
	s.written = append(s.written, kb.DatasetWrite{Spec: spec, Row: row})
	if s.writeErr == nil {
		existing := s.rows[spec.ResourceID]
		replaced := false
		for i, old := range existing {
			if old["_id"] == row["_id"] {
				existing[i] = row
				replaced = true
			}
		}
		if !replaced {
			existing = append(existing, row)
		}
		s.rows[spec.ResourceID] = existing
	}
	if spec.RelationTypeID == "" && s.writeErr == nil {
		if s.native == nil {
			s.native = map[string][]map[string]any{}
		}
		s.native[spec.ResourceID] = []map[string]any{row}
	}
	if s.afterWrite != nil {
		s.afterWrite()
	}
	return s.writeErr
}
func (s *systemLinkRows) Prune(ctx context.Context, spec interfaces.DraftDatasetSpec, scope kb.DatasetFileScope, ids []string, guard func(context.Context) error) error {
	if err := guard(ctx); err != nil {
		return err
	}
	s.pruned++
	kept := []map[string]any{}
	for _, row := range s.rows[spec.ResourceID] {
		id, _ := row["_id"].(string)
		keep := false
		for _, allowed := range ids {
			if id == allowed {
				keep = true
			}
		}
		if row["document_id"] != scope.DocumentID || row["content_version_id"] != scope.ContentVersionID || row["model_fingerprint"] != scope.ModelFingerprint || keep {
			kept = append(kept, row)
		}
	}
	s.rows[spec.ResourceID] = kept
	return nil
}

func systemLinkServiceFixture(t *testing.T) (*SystemLinkService, *systemLinkQueue, *systemLinkRows, *serviceSource, *systemAccess, *interfaces.Draft) {
	t.Helper()
	build, draft, input, task := extractionFixture(t, "contract", false)
	target := &interfaces.DraftObjectType{ID: "system_contract", Name: "Contract", Adopted: true, Table: interfaces.DraftTable{ResourceID: "system-table"}, PrimaryKeys: []string{"id"}, Properties: []*interfaces.DraftProperty{{Name: "id", Type: "unsigned integer", MappedField: "id"}, {Name: "number", Type: "string", MappedField: "number"}}}
	relation := &interfaces.DraftRelationType{ID: "same_contract", Name: "Same contract", Source: "contract_doc", Target: target.ID, Adopted: true, Cardinality: interfaces.DRAFT_CARDINALITY_ONE_TO_ONE, Document: &interfaces.DraftDocumentModel{Role: "relation"}, DocumentMapping: &interfaces.DraftDocumentMapping{Mode: "linking", MentionProperty: "number", IdentityProperty: "number", BusinessIdentity: &interfaces.DraftBusinessIdentityEvidence{TargetObjectTypeID: target.ID, ResourceID: target.Table.ResourceID, Property: "number", MappedField: "number", Type: "string", Rows: 1, Distinct: 1, ConfirmedBy: build.Owner, CheckedAt: 1}}}
	draft.Items.ObjectTypes = append(draft.Items.ObjectTypes, target)
	draft.Items.RelationTypes = append(draft.Items.RelationTypes, relation)
	plan, err := kbdataset.NewPlan(draft, build.GenerationID, "catalog", build.Owner)
	if err != nil {
		t.Fatal(err)
	}
	plan.State = "ready"
	for i := range plan.Datasets {
		plan.Datasets[i].Ready = true
	}
	draft.Items.DatasetPlans = []*interfaces.DraftDatasetPlan{plan}
	draft, err = kbdataset.BindPreview(draft, plan)
	if err != nil {
		t.Fatal(err)
	}
	build.ModelFingerprint = plan.ModelFingerprint
	writes, err := MaterializeFile(build, 0, draft, input, task)
	if err != nil {
		t.Fatal(err)
	}
	build.State, build.ModelID, build.Model = "succeeded", task.ModelID, task.Model
	build.Files[0].State, build.Files[0].AgentVersion, build.Files[0].PromptVersion, build.Files[0].ModelID = "succeeded", task.AgentVersion, task.PromptVersion, task.ModelID
	rows := &systemLinkRows{claimSnapshot: claimSnapshot{rows: map[string][]map[string]any{}}}
	for _, write := range writes {
		rows.rows[write.Spec.ResourceID] = append(rows.rows[write.Spec.ResourceID], write.Row)
	}
	queue := &systemLinkQueue{serviceQueue: serviceQueue{processMemory: processMemory{record: &interfaces.KBBuildRecord{Build: build, Identity: interfaces.KBBuildIdentity{Cleaned: true}}}}}
	ctrl := gomock.NewController(t)
	projects, selections, drafts := pmock.NewMockProjectService(ctrl), pmock.NewMockKBResourceAccess(ctrl), pmock.NewMockDraftAccess(ctrl)
	projects.EXPECT().GetProjectForStatus(gomock.Any(), "Bearer operator-token", "project", build.Owner).Return(&interfaces.ProjectEntry{Project: &interfaces.Project{ID: "project", KNID: build.NetworkID, Owner: build.Owner}}, false, nil).AnyTimes()
	selections.EXPECT().Read(gomock.Any(), "project").Return(&interfaces.KBResourceState{Current: &interfaces.KBSelection{ID: build.SelectionID, ProjectID: "project", Owner: build.Owner, State: "active", Revision: build.SelectionRevision, Files: []interfaces.KBResourceFile{build.Files[0].File}}}, nil).AnyTimes()
	drafts.EXPECT().GetDraft(gomock.Any(), "project").DoAndReturn(func(context.Context, string) (*interfaces.Draft, bool, error) { return draft, true, nil }).AnyTimes()
	source := &serviceSource{}
	access := &systemAccess{tables: []*interfaces.VegaTable{{ID: "system-table", Columns: []interfaces.VegaColumn{{Name: "id", Type: "unsigned integer"}, {Name: "number", Type: "string"}}}}, rows: []map[string]any{{"bkn_f_0": json.Number("18446744073709551615"), "bkn_f_1": "ABC"}}}
	service := &SystemLinkService{Builds: &BuildService{Projects: projects, Selections: selections, Drafts: drafts, Store: queue, Source: source}, Datasets: rows, Vega: access, Now: func() time.Time { return time.UnixMilli(build.UpdateTime + 100) }}
	return service, queue, rows, source, access, draft
}

func TestSystemLinkServiceWritesActualEdgesAndReplaysWithoutAnotherWrite(t *testing.T) {
	s, queue, data, _, _, _ := systemLinkServiceFixture(t)
	ctx, caller := context.Background(), queue.record.Build.Owner
	preview, err := s.Preview(ctx, "Bearer operator-token", "project", "job", caller)
	if err != nil || preview.Eligible != 1 || preview.Linked != 1 || len(data.written) != 0 {
		t.Fatal("preview did not use actual source/target snapshots", preview, err)
	}
	req := SystemLinkRequest{OperationID: uuid.NewString(), Signature: preview.Signature}
	for i := 0; i < 3; i++ {
		if i == 2 {
			req.OperationID = uuid.NewString()
		}
		result, err := s.Apply(ctx, "Bearer operator-token", "project", "job", caller, req)
		if err != nil || result.State != "succeeded" {
			t.Fatal("linking did not complete/replay", result, err)
		}
	}
	if len(data.written) != 1 || data.pruned != 1 || data.written[0].Row["target_record_id"] != json.Number("18446744073709551615") {
		t.Fatal("edge writes were repeated or primary key changed", len(data.written), data.pruned)
	}
	process, _ := json.Marshal(queue.record.Build)
	if strings.Contains(string(process), "ABC") || strings.Contains(string(process), "claim_value") || strings.Contains(string(process), "operator-token") {
		t.Fatal("instance values or credentials entered process storage")
	}
}

func TestSystemLinkServiceStopsOnRevocationCancellationAndChangedPreview(t *testing.T) {
	for _, scenario := range []string{"revoked", "cancelled", "preview_changed", "model_changed", "target_changed_during_write", "unmatched"} {
		t.Run(scenario, func(t *testing.T) {
			s, queue, data, source, access, draft := systemLinkServiceFixture(t)
			preview, err := s.Preview(context.Background(), "Bearer operator-token", "project", "job", queue.record.Build.Owner)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "revoked":
				data.afterWrite = func() { source.err = &kb.SourceError{StatusCode: 403} }
			case "cancelled":
				data.afterWrite = func() { queue.record.Build.State = "cancelled" }
			case "preview_changed":
				access.rows[0]["bkn_f_1"] = "changed"
			case "target_changed_during_write":
				data.afterWrite = func() { access.rows[0]["bkn_f_1"] = "changed" }
			case "model_changed":
				draft.UpdateTime++
			case "unmatched":
				access.rows = nil
				preview, err = s.Preview(context.Background(), "Bearer operator-token", "project", "job", queue.record.Build.Owner)
				if err != nil {
					t.Fatal(err)
				}
			}
			result, err := s.Apply(context.Background(), "Bearer operator-token", "project", "job", queue.record.Build.Owner, SystemLinkRequest{OperationID: uuid.NewString(), Signature: preview.Signature})
			if scenario == "unmatched" {
				if err != nil || result.Pending != 1 || len(data.written) != 0 || data.pruned != 1 {
					t.Fatal("unmatched source fabricated an edge or failed to remove stale links", result, err)
				}
				return
			}
			if err == nil || result != nil || data.pruned != 0 {
				t.Fatal("changed scope completed or deleted old links", scenario, err)
			}
			if scenario == "cancelled" && queue.record.Build.State != "cancelled" {
				t.Fatal("completion revived a cancelled task")
			}
			if scenario == "revoked" && !errors.As(err, new(*kb.SourceError)) {
				t.Fatal("source revocation was obscured", err)
			}
		})
	}
}

func TestSystemLinkServiceRecoversAnUncertainWriteAtTheSameRowIdentity(t *testing.T) {
	s, queue, data, _, _, _ := systemLinkServiceFixture(t)
	preview, err := s.Preview(context.Background(), "Bearer operator-token", "project", "job", queue.record.Build.Owner)
	if err != nil {
		t.Fatal(err)
	}
	request := SystemLinkRequest{OperationID: uuid.NewString(), Signature: preview.Signature}
	data.writeErr = errors.New("write response lost after storing the row")
	if result, err := s.Apply(context.Background(), "Bearer operator-token", "project", "job", queue.record.Build.Owner, request); err == nil || result != nil {
		t.Fatal("an uncertain write was treated as success")
	}
	if len(data.written) != 1 || data.pruned != 0 || queue.record.Build.Linking.State != "attention" {
		t.Fatal("uncertain write was retried or pruned", len(data.written), data.pruned)
	}
	first := data.written[0].Row["_id"]
	data.writeErr = nil
	result, err := s.Apply(context.Background(), "Bearer operator-token", "project", "job", queue.record.Build.Owner, request)
	if err != nil || result.State != "succeeded" || len(data.written) != 2 || data.written[1].Row["_id"] != first || data.pruned != 1 {
		t.Fatal("recovery changed identity or left partial links", result, err)
	}
}

func (s *systemLinkRows) ReadEntityRows(ctx context.Context, spec interfaces.DraftDatasetSpec, _ string, guard func(context.Context) error) ([]map[string]any, error) {
	if err := guard(ctx); err != nil {
		return nil, err
	}
	return s.native[spec.ResourceID], nil
}
func (s *systemLinkRows) PruneEntities(ctx context.Context, _ interfaces.DraftDatasetSpec, _ string, _ []string, guard func(context.Context) error) error {
	if err := guard(ctx); err != nil {
		return err
	}
	s.nativePruned++
	return nil
}
func TestFullBuildWritesCurrentValuesAndLinksUnderTheSameFencedOperation(t *testing.T) {
	service, queue, data, _, _, _ := systemLinkServiceFixture(t)
	queue.record.Build.Mode = "full"
	preview, err := service.Preview(context.Background(), "Bearer operator-token", "project", "job", queue.record.Build.Owner)
	if err != nil || preview.FusionPreviewOnly || len(preview.Fusion) != 1 || preview.Fusion[0].Current != 2 {
		t.Fatal("full fusion preview missing", preview, err)
	}
	request := SystemLinkRequest{OperationID: "a93810f5-7e60-433d-9fa8-d178089aa029", Signature: preview.Signature}
	receipt, err := service.Apply(context.Background(), "Bearer operator-token", "project", "job", queue.record.Build.Owner, request)
	if err != nil || !receipt.FusionApplied || receipt.FusionCurrent != 2 || receipt.FusionPending != 0 || len(data.written) != 2 || data.nativePruned != 1 {
		t.Fatal("native fusion was not committed with its links", receipt, len(data.written), err)
	}
	again, err := service.Preview(context.Background(), "Bearer operator-token", "project", "job", queue.record.Build.Owner)
	if err != nil || again.Signature != preview.Signature {
		t.Fatal("own writes invalidated the reviewed recipe", again, err)
	}
	request.OperationID = "73d9aee2-96c5-444b-92d7-b26a27a30789"
	_, err = service.Apply(context.Background(), "Bearer operator-token", "project", "job", queue.record.Build.Owner, request)
	if err != nil || len(data.written) != 2 {
		t.Fatal("unchanged full fusion wrote rows again", err)
	}
}
func TestSampleBuildPreviewsCurrentValuesWithoutChangingCanonicalGeneration(t *testing.T) {
	service, queue, data, _, _, _ := systemLinkServiceFixture(t)
	preview, err := service.Preview(context.Background(), "Bearer operator-token", "project", "job", queue.record.Build.Owner)
	if err != nil || !preview.FusionPreviewOnly || len(preview.Fusion) != 1 {
		t.Fatal("sample preview missing", preview, err)
	}
	receipt, err := service.Apply(context.Background(), "Bearer operator-token", "project", "job", queue.record.Build.Owner, SystemLinkRequest{OperationID: "a93810f5-7e60-433d-9fa8-d178089aa029", Signature: preview.Signature})
	if err != nil || receipt.FusionApplied || len(data.native) != 0 || data.nativePruned != 0 {
		t.Fatal("sample altered canonical full-generation rows", receipt, err)
	}
}

type uncertainEntityRows struct {
	*systemLinkRows
	lost bool
}

func (s *uncertainEntityRows) Replace(ctx context.Context, spec interfaces.DraftDatasetSpec, row map[string]any) error {
	if err := s.systemLinkRows.Replace(ctx, spec, row); err != nil {
		return err
	}
	if spec.RelationTypeID == "" && !s.lost {
		s.lost = true
		return kb.ErrUnavailable
	}
	return nil
}
func TestFullFusionResumesAnAlreadyAppliedButUnacknowledgedEntityWrite(t *testing.T) {
	service, queue, data, _, _, _ := systemLinkServiceFixture(t)
	queue.record.Build.Mode = "full"
	store := &uncertainEntityRows{systemLinkRows: data}
	service.Datasets = store
	preview, err := service.Preview(context.Background(), "Bearer operator-token", "project", "job", queue.record.Build.Owner)
	if err != nil {
		t.Fatal(err)
	}
	request := SystemLinkRequest{OperationID: "a93810f5-7e60-433d-9fa8-d178089aa029", Signature: preview.Signature}
	_, err = service.Apply(context.Background(), "Bearer operator-token", "project", "job", queue.record.Build.Owner, request)
	if err == nil || queue.record.Build.State != "linking_attention" || data.nativePruned != 0 {
		t.Fatal("uncertain entity write was reported complete", err)
	}
	receipt, err := service.Apply(context.Background(), "Bearer operator-token", "project", "job", queue.record.Build.Owner, request)
	if err != nil || !receipt.FusionApplied || queue.record.Build.State != "succeeded" {
		t.Fatal("same intent did not safely resume", receipt, err)
	}
	identities := map[string]bool{}
	for _, write := range data.written {
		identities[write.Spec.ResourceID+"/"+write.Row["_id"].(string)] = true
	}
	if len(identities) != 2 {
		t.Fatal("retry changed logical row identities", len(identities))
	}
}

func TestFullFusionConnectsFileClaimsToTheirNativeEntityEvenWhenAgentOmittedTheEdge(t *testing.T) {
	testFullNativeFusion(t, 0)
}
func TestNativeFusionRetainsAdditionalGroundedAgentRelationEvidenceAfterPrune(t *testing.T) {
	testFullNativeFusion(t, 1)
}
func TestNativeFusionRetainsMoreThanFortyLocationsAcrossDistinctExtractedEdges(t *testing.T) {
	testFullNativeFusion(t, 3)
}
func testFullNativeFusion(t *testing.T, relationEvidence int) {
	t.Helper()
	service, queue, data, _, _, draft := systemLinkServiceFixture(t)
	build := queue.record.Build
	build.Mode = "full"
	data.wire = true
	draft.Items.RelationTypes = append(draft.Items.RelationTypes, &interfaces.DraftRelationType{ID: "file_contract", Name: "File mentions contract", Adopted: true, Source: "contract_file", Target: "contract_doc", Cardinality: interfaces.DRAFT_CARDINALITY_MANY_TO_ONE, Document: &interfaces.DraftDocumentModel{Role: "relation"}, DocumentMapping: &interfaces.DraftDocumentMapping{Mode: "linking", MentionProperty: "number", IdentityProperty: "number"}})
	plan, err := kbdataset.NewPlan(draft, build.GenerationID, "catalog", build.Owner)
	if err != nil {
		t.Fatal(err)
	}
	plan.State = "ready"
	for i := range plan.Datasets {
		plan.Datasets[i].Ready = true
	}
	draft.Items.DatasetPlans = []*interfaces.DraftDatasetPlan{plan}
	bound, err := kbdataset.BindPreview(draft, plan)
	if err != nil {
		t.Fatal(err)
	}
	*draft = *bound
	build.ModelFingerprint = plan.ModelFingerprint
	_, _, input, task := extractionFixture(t, "contract", false)

	if relationEvidence > 0 {
		var proposal FileExtraction
		if err := json.Unmarshal([]byte(task.Output), &proposal); err != nil {
			t.Fatal(err)
		}
		if relationEvidence == 1 {
			proposal.Relations = append(proposal.Relations, InstanceRelation{RelationTypeID: "file_contract", Source: "file", Target: "current", Evidence: []InstanceCitation{{BlockID: "native-1", Quote: "Reference ABC."}}})
		} else {
			text := input.Text["native-1"]
			quotes := []InstanceCitation{}
			for start := 0; start < len(text) && len(quotes) < 60; start++ {
				for end := start + 4; end <= len(text) && len(quotes) < 60; end++ {
					quote := text[start:end]
					if strings.Count(text, quote) == 1 {
						quotes = append(quotes, InstanceCitation{BlockID: "native-1", Quote: quote})
					}
				}
			}
			if len(quotes) < 60 {
				t.Fatal("fixture needs sixty distinct grounded locations")
			}
			for index := 0; index < 3; index++ {
				proposal.Relations = append(proposal.Relations, InstanceRelation{RelationTypeID: "file_contract", Source: "file", Target: "current", Evidence: quotes[index*20 : (index+1)*20]})
			}
		}

		encoded, err := json.Marshal(proposal)
		if err != nil {
			t.Fatal(err)
		}
		task.Output = string(encoded)
	}
	extracted, err := MaterializeFile(build, 0, draft, input, task)
	if err != nil {
		t.Fatal(err)
	}
	data.rows = map[string][]map[string]any{}
	for _, write := range extracted {
		data.rows[write.Spec.ResourceID] = append(data.rows[write.Spec.ResourceID], write.Row)
	}
	preview, err := service.Preview(context.Background(), "Bearer operator-token", "project", "job", build.Owner)
	if err != nil || len(preview.Relations) != 2 || preview.Linked != 2 {
		t.Fatal("native entity links absent", preview, err)
	}
	_, err = service.Apply(context.Background(), "Bearer operator-token", "project", "job", build.Owner, SystemLinkRequest{OperationID: "a93810f5-7e60-433d-9fa8-d178089aa029", Signature: preview.Signature})

	expectedWrites := 3
	if relationEvidence > 0 {
		expectedWrites = 2
	}
	if err != nil || len(data.written) != expectedWrites || data.nativePruned != 1 {
		t.Fatal("native relation did not share the fusion boundary", len(data.written), err)
	}
	entityID := ""
	targetID := ""
	for _, write := range data.written {
		if write.Spec.ObjectTypeID == "contract_doc" {
			entityID = write.Row["_id"].(string)
		}
	}
	locations := 0
	for _, spec := range plan.Datasets {
		if spec.RelationTypeID != "file_contract" {
			continue
		}
		expectedRows := max(1, relationEvidence)
		if len(data.rows[spec.ResourceID]) != expectedRows {
			t.Fatal("lost extracted observations or added duplicate fallback", len(data.rows[spec.ResourceID]))
		}
		for _, row := range data.rows[spec.ResourceID] {
			targetID = row["target_record_id"].(string)
			identity := row["target_identity"].(map[string]any)
			if identity["number"] != "ABC" {
				t.Fatal("business identity replaced with storage identity")
			}
			raw, _ := json.Marshal(row["evidence"])
			var proof []locatedEvidence
			if json.Unmarshal(raw, &proof) != nil {
				t.Fatal("unreadable evidence")
			}
			locations += len(proof)
			for _, location := range proof {
				if location.Quote != "" {
					t.Fatal("original-off evidence leaked text")
				}
			}
		}
	}
	if relationEvidence == 3 && locations != 60 {
		t.Fatal("grounded evidence was truncated", locations)
	}

	if entityID == "" || targetID != entityID {
		t.Fatal("native edge target is not the materialized entity", entityID, targetID)
	}
	repeated, err := service.Preview(context.Background(), "Bearer operator-token", "project", "job", build.Owner)
	if err != nil || repeated.Signature != preview.Signature {
		t.Fatal("preserved evidence was unstable after native writes and pruning", err)
	}

}

func TestFullFusionCannotPruneFromAnIncompleteSelectionManifest(t *testing.T) {
	service, queue, data, _, _, _ := systemLinkServiceFixture(t)
	build := queue.record.Build
	build.Mode = "full"
	ctrl := gomock.NewController(t)
	selections := pmock.NewMockKBResourceAccess(ctrl)
	extra := build.Files[0].File
	extra.FileID = "11"
	selections.EXPECT().Read(gomock.Any(), "project").Return(&interfaces.KBResourceState{Current: &interfaces.KBSelection{ID: build.SelectionID, Revision: build.SelectionRevision, Owner: build.Owner, State: "active", Files: []interfaces.KBResourceFile{build.Files[0].File, extra}}}, nil).AnyTimes()
	service.Builds.Selections = selections
	preview, err := service.Preview(context.Background(), "Bearer operator-token", "project", "job", build.Owner)
	if err == nil || preview != nil || len(data.written) != 0 || data.nativePruned != 0 {
		t.Fatal("partial manifest qualified as complete canonical input", preview, err)
	}
}
