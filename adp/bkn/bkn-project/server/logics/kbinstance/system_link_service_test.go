// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
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
	claimSnapshot
	written    []kb.DatasetWrite
	pruned     int
	afterWrite func()
	writeErr   error
}

func (s *systemLinkRows) Replace(_ context.Context, spec interfaces.DraftDatasetSpec, row map[string]any) error {
	s.written = append(s.written, kb.DatasetWrite{Spec: spec, Row: row})
	if s.afterWrite != nil {
		s.afterWrite()
	}
	return s.writeErr
}
func (s *systemLinkRows) Prune(ctx context.Context, _ interfaces.DraftDatasetSpec, _ kb.DatasetFileScope, _ []string, guard func(context.Context) error) error {
	if err := guard(ctx); err != nil {
		return err
	}
	s.pruned++
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
