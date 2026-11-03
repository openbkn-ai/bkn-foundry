// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbconcept"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbdataset"
)

func factServiceFixture(t *testing.T, mode string, confirmed ...bool) (*SystemLinkService, *systemLinkQueue, *systemLinkRows, *interfaces.Draft, string) {
	t.Helper()
	service, queue, data, _, _, draft := systemLinkServiceFixture(t)
	build := queue.record.Build
	build.Mode = mode
	_, _, input, task := extractionFixture(t, "contract", false)
	fact := instanceObject(draft.Items, "contract_file")
	fact.Document.Role = "fact"
	fact.Document.FileIdentity = nil
	for _, field := range []struct{ name, typ string }{{"effective_from", "datetime"}, {"effective_to", "datetime"}, {"recorded_at", "datetime"}, {"status", "string"}} {
		fact.Properties = append(fact.Properties, &interfaces.DraftProperty{Name: field.name, Type: field.typ, ReadOnly: true, SourceOfRecord: "document"})
	}
	relation := &interfaces.DraftRelationType{ID: "fact_for_contract", Name: "Applies to contract", Source: fact.ID, Target: "contract_doc", Adopted: true, Cardinality: "many_to_one", Document: &interfaces.DraftDocumentModel{Role: "relation"}, DocumentMapping: &interfaces.DraftDocumentMapping{Mode: "linking", MentionProperty: "number", IdentityProperty: "number"}}
	draft.Items.RelationTypes = append(draft.Items.RelationTypes, relation)
	if err := kbconcept.ConfirmFactResolution(draft.Items, fact, interfaces.FactResolutionDecision{ValueProperty: "amount", SubjectRelationID: relation.ID, Confirm: len(confirmed) == 0 || confirmed[0]}, build.Owner, 10); err != nil {
		t.Fatal(err)
	}
	plan, err := kbdataset.NewPlan(draft, "generation", "catalog", build.Owner)
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
	input.Text["native-1"] += " Valid since 2020-01-01T00:00:00Z."
	input.Blocks[0].End = utf8.RuneCountInString(input.Text["native-1"])
	build.Files[0].InputDigest, err = FileInputDigest(fileRef(build.Files[0].File), input)
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := parseFileExtraction(task.Output)
	if err != nil {
		t.Fatal("invalid fixture", err)
	}
	for i := range proposal.Records {
		record := &proposal.Records[i]
		if record.ObjectTypeID == fact.ID {
			record.Anchor.Quote = "Total 25.50."
			record.Values["effective_from"] = InstanceValue{Value: "2020-01-01T00:00:00Z", Evidence: []InstanceCitation{{BlockID: "native-1", Quote: "2020-01-01T00:00:00Z"}}}
			record.Values["effective_to"] = InstanceValue{Value: nil, Evidence: []InstanceCitation{}}
		}
	}
	raw, _ := json.Marshal(proposal)
	task.Output = string(raw)
	writes, err := MaterializeFile(build, 0, draft, input, task)
	if err != nil {
		t.Fatal(err)
	}
	data.rows = map[string][]map[string]any{}
	data.native = nil
	id := ""
	for _, write := range writes {
		data.rows[write.Spec.ResourceID] = append(data.rows[write.Spec.ResourceID], write.Row)
		if write.Spec.ObjectTypeID == fact.ID {
			id = write.Row["_id"].(string)
		}
	}
	service.Now = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	data.wire = true
	return service, queue, data, draft, id
}

func TestFactStatusUsesRealMaterializationClaimsLinksAndFencedSave(t *testing.T) {
	for _, mode := range []string{"debug", "acceptance", "full"} {
		t.Run(mode, func(t *testing.T) {
			service, queue, data, _, id := factServiceFixture(t, mode)
			caller := queue.record.Build.Owner
			preview, err := service.Preview(context.Background(), "Bearer operator-token", "project", "job", caller)
			if err != nil || len(preview.Facts) != 1 || preview.Facts[0].Current != 1 {
				t.Fatal("fact preview did not use native dates and entity links", preview, err)
			}
			result, err := service.Apply(context.Background(), "Bearer operator-token", "project", "job", caller, SystemLinkRequest{OperationID: uuid.NewString(), Signature: preview.Signature})
			if err != nil || result.State != "succeeded" || result.FactCurrent != 1 {
				t.Fatal("fact save failed", result, err)
			}
			found, written := false, 0
			for _, write := range data.written {
				if write.Spec.ObjectTypeID == "contract_file" {
					written++
					if write.Row["_id"] != id || write.Row["status"] != "current" || write.Row["source_quote"] != "" || write.Row["amount"] != json.Number("25.50") {
						t.Fatal("fact identity, value or source privacy changed", write.Row)
					}
				}
			}
			for _, rows := range data.rows {
				for _, row := range rows {
					if row["_id"] == id {
						found = true
						expected := "pending_review"
						if mode == "full" {
							expected = "current"
						}
						if row["status"] != expected {
							t.Fatal("sample wrote canonical fact state", mode, row["status"])
						}
					}
				}
			}
			if !found || (written == 1) != (mode == "full") {
				t.Fatal("wrong fact write boundary", mode, written)
			}
			again, err := service.Preview(context.Background(), "Bearer operator-token", "project", "job", caller)
			if err != nil || again.Signature != preview.Signature {
				t.Fatal("fact save changed reviewed signature", again, err)
			}
			data.written = nil
			if _, err := service.Apply(context.Background(), "Bearer operator-token", "project", "job", caller, SystemLinkRequest{OperationID: uuid.NewString(), Signature: again.Signature}); err != nil {
				t.Fatal(err)
			}
			for _, write := range data.written {
				if write.Spec.ObjectTypeID == "contract_file" {
					t.Fatal("unchanged fact was rewritten")
				}
			}
		})
	}
}

func TestFactStatusRejectsMissingRowsAndChangedExecutionBeforeAnyWrite(t *testing.T) {
	for _, scenario := range []string{"missing_row", "changed_execution", "changed_value", "partial_read"} {
		service, queue, data, draft, _ := factServiceFixture(t, "full")
		filePlan, err := newFileRows(queue.record.Build, 0, draft, "")
		spec := filePlan.objects["contract_file"]
		if err != nil {
			t.Fatal(err)
		}
		switch scenario {
		case "missing_row":
			delete(data.rows, spec.ResourceID)
		case "changed_execution":
			data.rows[spec.ResourceID][0]["extraction_version"] = `{"task_id":"foreign"}`
		case "changed_value":
			data.rows[spec.ResourceID][0]["amount"] = json.Number("99")
		case "partial_read":
			data.err = errors.New("partial snapshot")
		}
		if _, err := service.Preview(context.Background(), "Bearer operator-token", "project", "job", queue.record.Build.Owner); err == nil || len(data.written) != 0 {
			t.Fatal("unsafe fact snapshot accepted", scenario, err)
		}
	}
}

func TestFactStateArbitrationIsTemporalScopedAndIndependentOfOrder(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	for _, domain := range []string{"contract", "patent", "maintenance"} {
		for _, scenario := range []string{"newer", "same_time_conflict", "same_value", "missing_time", "future", "expired", "invalid_period", "other_subject", "no_subject"} {
			t.Run(domain+"/"+scenario, func(t *testing.T) {
				object := &interfaces.DraftObjectType{ID: domain + "_fact", Document: &interfaces.DraftDocumentModel{Role: "fact", FactResolution: &interfaces.FactResolutionRule{ValueProperty: "value"}}, Properties: []*interfaces.DraftProperty{{Name: "value", Type: "string"}, {Name: "effective_from", Type: "datetime", ReadOnly: true}, {Name: "effective_to", Type: "datetime", ReadOnly: true}}}
				makeRow := func(id, value, from string) factStateRow {
					return factStateRow{object: object, subject: "entity-A", write: kb.DatasetWrite{Row: map[string]any{"_id": id}}, claims: map[string]SourceClaim{"value": {Property: "value", Value: value, RecordedAt: 10}, "effective_from": {Property: "effective_from", Value: from}}}
				}
				first := makeRow("old", "before", "2024-01-01T00:00:00Z")
				second := makeRow("new", "after", "2025-01-01T00:00:00Z")
				expectedOld, expectedNew := "superseded", "current"
				switch scenario {
				case "same_time_conflict":
					second.claims["effective_from"] = first.claims["effective_from"]
					expectedOld, expectedNew = "pending_review", "pending_review"
				case "same_value":
					second.claims["effective_from"] = first.claims["effective_from"]
					second.claims["value"] = first.claims["value"]
					expectedOld, expectedNew = "current", "current"
				case "missing_time":
					delete(second.claims, "effective_from")
					expectedOld, expectedNew = "pending_review", "pending_review"
				case "future":
					second.claims["effective_from"] = SourceClaim{Value: "2027-01-01T00:00:00Z"}
					expectedOld, expectedNew = "current", "pending_review"
				case "expired":
					second.claims["effective_to"] = SourceClaim{Value: "2025-12-01T00:00:00Z"}
					expectedOld, expectedNew = "current", "superseded"
				case "invalid_period":
					second.claims["effective_to"] = SourceClaim{Value: "2024-01-01T00:00:00Z"}
					expectedOld, expectedNew = "current", "pending_review"
				case "other_subject":
					second.subject = "entity-B"
					expectedOld, expectedNew = "current", "current"
				case "no_subject":
					second.subject = ""
					expectedOld, expectedNew = "current", "pending_review"
				}
				for _, rows := range [][]factStateRow{{first, second}, {second, first}} {
					states, err := resolveFactStates(rows, now)
					if err != nil || states[object.ID+"/old"] != expectedOld || states[object.ID+"/new"] != expectedNew {
						t.Fatal("wrong temporal fact states", states, err)
					}
				}
			})
		}
	}
}

type unconfirmedFactReadGuard struct{ *systemLinkRows }

func (data unconfirmedFactReadGuard) ReadFileRows(ctx context.Context, spec interfaces.DraftDatasetSpec, scope kb.DatasetFileScope, guard func(context.Context) error) ([]map[string]any, error) {
	if spec.ObjectTypeID == "contract_file" {
		return nil, errors.New("unconfirmed facts must not consume fact-read/write budgets")
	}
	return data.systemLinkRows.ReadFileRows(ctx, spec, scope, guard)
}
func TestUnconfirmedFactsStayPendingWithoutBeingReadOrAssignedFakeCounts(t *testing.T) {
	service, queue, data, _, id := factServiceFixture(t, "full", false)
	service.Datasets = unconfirmedFactReadGuard{data}
	preview, err := service.Preview(context.Background(), "Bearer operator-token", "project", "job", queue.record.Build.Owner)
	if err != nil || len(preview.Facts) != 0 || len(preview.FactRulesPending) != 1 {
		t.Fatal("unconfirmed facts consumed the operation or invented counts", preview, err)
	}
	for _, rows := range data.rows {
		for _, row := range rows {
			if row["_id"] == id && row["status"] != "pending_review" {
				t.Fatal("unconfirmed fact gained current authority")
			}
		}
	}
}
