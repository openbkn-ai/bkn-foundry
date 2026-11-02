// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbdataset

import (
	"context"
	"errors"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func preparationObject(id string) *interfaces.DraftObjectType {
	return &interfaces.DraftObjectType{ID: id, Name: id, Adopted: true, Document: &interfaces.DraftDocumentModel{Role: "fact", SelectionID: "selection", SelectionRevision: 3}, PrimaryKeys: []string{"_id"}, DisplayKey: "name", Properties: []*interfaces.DraftProperty{{Name: "_id", Type: "string", ReadOnly: true}, {Name: "name", Type: "string", Extraction: &interfaces.DraftExtractionField{Instruction: "Extract the named business fact"}}}}
}

type preparationStorage struct {
	calls  []interfaces.DraftDatasetSpec
	failAt int
}

func (s *preparationStorage) Catalog(context.Context, string) (string, error) {
	return "private-catalog", nil
}
func (s *preparationStorage) Ensure(_ context.Context, spec interfaces.DraftDatasetSpec) error {
	s.calls = append(s.calls, spec)
	if len(s.calls) == s.failAt {
		return errors.New("uncertain remote completion")
	}
	return nil
}

func TestPreparationRetainsIntentAndResumesWithoutPartialBinding(t *testing.T) {
	ctx := context.Background()
	draft := &interfaces.Draft{ProjectID: "p", Items: interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{preparationObject("delivery_fact"), preparationObject("status_fact")}}}
	plan, err := NewPlan(draft, "generation-one", "private-catalog", interfaces.AccountInfo{ID: "owner", Type: "user"})
	if err != nil {
		t.Fatal(err)
	}
	draft.Items.DatasetPlans = []*interfaces.DraftDatasetPlan{plan}
	persisted := 0
	save := func(context.Context, *interfaces.Draft) error { persisted++; return nil }
	check := func(context.Context, *interfaces.Draft) error { return nil }
	storage := &preparationStorage{failAt: 2}
	if err := Prepare(ctx, draft, plan, storage, save, check); err == nil {
		t.Fatal("uncertain write accepted")
	}
	if persisted < 2 || !plan.Datasets[0].Ready || plan.Datasets[1].Ready || plan.State != "failed" {
		t.Fatalf("lost preparation journal: %+v", plan)
	}
	for _, o := range draft.Items.ObjectTypes {
		if o.Table.ResourceID != "" {
			t.Fatal("partially bound definitions")
		}
	}
	ids := []string{plan.Datasets[0].ResourceID, plan.Datasets[1].ResourceID}
	storage.failAt = 0
	if err := Prepare(ctx, draft, plan, storage, save, check); err != nil {
		t.Fatal(err)
	}
	if plan.State != "ready" || len(storage.calls) != 4 {
		t.Fatal("resume skipped verification or created a new generation")
	}
	for i, o := range draft.Items.ObjectTypes {
		if o.Table.ResourceID != ids[i] || o.Properties[0].MappedField != "bkn_record_id" {
			t.Fatalf("invalid binding: %+v", o)
		}
	}
	fingerprint, err := ModelFingerprint(draft)
	if err != nil || fingerprint != plan.ModelFingerprint {
		t.Fatal("physical bindings changed the extraction fingerprint")
	}
}

func TestPreparationRejectsModelChangesAndRevocationBeforeBind(t *testing.T) {
	for _, cause := range []string{"model", "revoked"} {
		t.Run(cause, func(t *testing.T) {
			draft := &interfaces.Draft{ProjectID: "p", Items: interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{preparationObject("audit_fact")}}}
			plan, err := NewPlan(draft, "generation", "private-catalog", interfaces.AccountInfo{ID: "owner", Type: "user"})
			if err != nil {
				t.Fatal(err)
			}
			checks := 0
			check := func(context.Context, *interfaces.Draft) error {
				checks++
				if checks == 3 {
					if cause == "revoked" {
						return errors.New("permission revoked")
					}
					draft.Items.ObjectTypes[0].Properties[1].Extraction.Instruction = "Changed by operator"
				}
				return nil
			}
			storage := &preparationStorage{}
			err = Prepare(context.Background(), draft, plan, storage, func(context.Context, *interfaces.Draft) error { return nil }, check)
			if err == nil || draft.Items.ObjectTypes[0].Table.ResourceID != "" {
				t.Fatal("late binding accepted")
			}
		})
	}
}

func TestPlanUsesNewStorageForAnotherTargetGeneration(t *testing.T) {
	draft := &interfaces.Draft{ProjectID: "p", Items: interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{preparationObject("event_fact")}}}
	a, err := NewPlan(draft, "a", "catalog", interfaces.AccountInfo{ID: "owner", Type: "user"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewPlan(draft, "b", "catalog", interfaces.AccountInfo{ID: "owner", Type: "user"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Datasets[0].ResourceID == b.Datasets[0].ResourceID {
		t.Fatal("unpublished generation shares writable published storage")
	}
}

func TestPreparationRejectsCorruptSavedResourcePlanBeforeRemoteCall(t *testing.T) {
	for _, mutation := range []string{"resource", "mapping", "selection", "owner"} {
		t.Run(mutation, func(t *testing.T) {
			draft := &interfaces.Draft{ProjectID: "p", Items: interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{preparationObject("quality_fact")}}}
			plan, err := NewPlan(draft, "generation", "catalog", interfaces.AccountInfo{ID: "owner", Type: "user"})
			if err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "resource":
				plan.Datasets[0].ResourceID = "foreign-resource"
			case "mapping":
				plan.Datasets[0].Layout.Mapping["name"] = "unrelated_field"
			case "selection":
				plan.SelectionRevision++
			case "owner":
				plan.Datasets[0].Owner.ID = "someone-else"
			}
			storage := &preparationStorage{}
			err = Prepare(context.Background(), draft, plan, storage, func(context.Context, *interfaces.Draft) error { return nil }, func(context.Context, *interfaces.Draft) error { return nil })
			if err == nil || len(storage.calls) != 0 {
				t.Fatal("invalid plan reached remote storage")
			}
		})
	}
}

func TestPreparationFailedFinalCompareAndWriteLeavesModelUnbound(t *testing.T) {
	draft := &interfaces.Draft{ProjectID: "p", Items: interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{preparationObject("legal_fact")}}}
	plan, err := NewPlan(draft, "generation", "catalog", interfaces.AccountInfo{ID: "owner", Type: "user"})
	if err != nil {
		t.Fatal(err)
	}
	save := func(_ context.Context, value *interfaces.Draft) error {
		if value.Items.ObjectTypes[0].Table.ResourceID != "" {
			return errors.New("concurrent project archive")
		}
		return nil
	}
	err = Prepare(context.Background(), draft, plan, &preparationStorage{}, save, func(context.Context, *interfaces.Draft) error { return nil })
	if err == nil || draft.Items.ObjectTypes[0].Table.ResourceID != "" {
		t.Fatal("failed final guard exposed binding")
	}
}

func TestPreparationGivesGroundedBusinessRelationsTheirOwnDataset(t *testing.T) {
	source, target := preparationObject("source_fact"), preparationObject("target_fact")
	relation := &interfaces.DraftRelationType{ID: source.ID, Name: "Business relation", Source: source.ID, Target: target.ID, Adopted: true, Cardinality: interfaces.DRAFT_CARDINALITY_MANY_TO_MANY, Document: &interfaces.DraftDocumentModel{Role: "relation"}, DocumentMapping: &interfaces.DraftDocumentMapping{Mode: "same_document", SourceNameProperty: "name", TargetNameProperty: "name"}}
	draft := &interfaces.Draft{ProjectID: "p", Items: interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{source, target}, RelationTypes: []*interfaces.DraftRelationType{relation}}}
	plan, err := NewPlan(draft, "generation", "private-catalog", interfaces.AccountInfo{ID: "owner", Type: "user"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Datasets) != 3 {
		t.Fatal("missing relation data storage")
	}
	bound, err := BindPreview(draft, plan)
	if err != nil {
		t.Fatal(err)
	}
	edge := bound.Items.RelationTypes[0]
	if edge.LinkTable == nil || edge.LinkTable.ResourceID == "" || len(edge.SourceMapping) != 1 || len(edge.TargetMapping) != 1 {
		t.Fatal("no indirect evidence binding")
	}
	if edge.SourceMapping[0].SourceProperty != "_id" || edge.SourceMapping[0].TargetProperty != "source_record_id" || edge.TargetMapping[0].SourceProperty != "target_record_id" || edge.TargetMapping[0].TargetProperty != "_id" {
		t.Fatal("endpoint-to-backing-resource mapping reversed")
	}
	proof := false
	for _, p := range edge.Properties {
		if p.Name == "evidence" {
			proof = p.Type == "json" && p.ReadOnly
		}
	}
	if !proof {
		t.Fatal("business relation has no independent supporting evidence")
	}
	hash, err := ModelFingerprint(bound)
	if err != nil || hash != plan.ModelFingerprint {
		t.Fatal("derived relationship binding changed model fingerprint")
	}
}

func TestSentenceKeyChangesInvalidateTheModelButAuditRefreshDoesNot(t *testing.T) {
	object := preparationObject("fact")
	draft := &interfaces.Draft{ProjectID: "p", Items: interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{object}}}
	original, err := ModelFingerprint(draft)
	if err != nil {
		t.Fatal(err)
	}
	object.Document.SentenceIdentity = &interfaces.SentenceBusinessIdentity{Properties: []string{"name"}, ConfirmedBy: interfaces.AccountInfo{ID: "owner", Type: "user"}, ConfirmedAt: 10}
	changed, err := ModelFingerprint(draft)
	if err != nil || original == changed {
		t.Fatal("sentence identity was excluded from the model signature", err)
	}
	object.Document.SentenceIdentity.ConfirmedAt = 20
	object.Document.SentenceIdentity.ConfirmedBy.ID = "another-reviewer"
	refreshed, err := ModelFingerprint(draft)
	if err != nil || changed != refreshed {
		t.Fatal("audit refresh invalidated the extraction model", err)
	}
}
