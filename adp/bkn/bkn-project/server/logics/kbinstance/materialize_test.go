// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbconcept"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbdataset"
)

func extractionFixture(t *testing.T, domain string, original bool) (*interfaces.KBBuild, *interfaces.Draft, *kb.FileInput, *interfaces.BknAgentTask) {
	t.Helper()
	owner := interfaces.AccountInfo{ID: "owner", Type: "user"}
	text := "Reference ABC. Total 25.50."
	ref := kb.SampleVersion{KnowledgeBaseID: "1", VersionRef: kb.VersionRef{FileID: "10", ContentVersionID: "100"}}
	input := &kb.FileInput{Blocks: []kb.EvidenceBlock{{SampleVersion: ref, BlockID: "native-1", Start: 0, End: utf8.RuneCountInString(text)}}, Media: []kb.EvidenceMedia{}, Text: map[string]string{"native-1": text}}
	properties := func() []*interfaces.DraftProperty {
		result := []*interfaces.DraftProperty{{Name: "number", Type: "string"}, {Name: "amount", Type: "float"}}
		for _, field := range []struct{ name, typ string }{{"_id", "string"}, {"entity_id", "string"}, {"document_id", "string"}, {"content_version_id", "string"}, {"block_id", "string"}, {"source_start", "integer"}, {"source_end", "integer"}, {"source_quote", "text"}, {"extraction_version", "string"}, {"model_fingerprint", "string"}} {
			result = append(result, &interfaces.DraftProperty{Name: field.name, Type: field.typ, ReadOnly: true, SourceOfRecord: "document"})
		}
		return result
	}
	entity := &interfaces.DraftObjectType{ID: domain + "_doc", Name: domain, Adopted: true, PrimaryKeys: []string{"number"}, Document: &interfaces.DraftDocumentModel{Role: "entity", Layer: "entity", SelectionID: "selection", SelectionRevision: 2}, Properties: properties()}
	file := &interfaces.DraftObjectType{ID: domain + "_file", Name: domain + " file", Adopted: true, PrimaryKeys: []string{"_id"}, Document: &interfaces.DraftDocumentModel{Role: "file", Layer: "fact", SelectionID: "selection", SelectionRevision: 2}, Properties: properties()}
	if err := kbconcept.ConfirmFileIdentity(file, interfaces.FileIdentityDecision{Property: "number", Confirm: true}, owner, 10); err != nil {
		t.Fatal(err)
	}
	items := interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{entity, file}}
	evidence, err := kbconcept.BuildEvidence(&kbconcept.SampleInput{SelectionID: "selection", SelectionRevision: 2, Request: kb.ConceptRequest{NamingLanguage: "en-US"}}, items)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range evidence.Model.ObjectTypes {
		o.Adopted = true
		items.ObjectTypes = append(items.ObjectTypes, o)
	}
	for _, r := range evidence.Model.RelationTypes {
		r.Adopted = true
		items.RelationTypes = append(items.RelationTypes, r)
	}
	draft := &interfaces.Draft{ProjectID: "project", Status: "ready", UpdateTime: 10, Items: items}
	draft.Items.Confirmation = &interfaces.DraftConfirmation{UpdateTime: 10, Commit: "commit"}
	plan, err := kbdataset.NewPlan(draft, "generation", "catalog", owner)
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
	selection := &interfaces.KBSelection{ID: "selection", ProjectID: "project", State: "active", Revision: 2, Owner: owner, Files: []interfaces.KBResourceFile{{KnowledgeBaseID: "1", FileID: "10", ContentVersionID: "100", Name: "input.pdf", IncludeOriginal: original, SampleRole: "debug"}}}
	build, err := PlanFileBuild("job", "lineage", "debug", 3, draft, selection)
	if err != nil {
		t.Fatal(err)
	}
	build.Files[0].InputDigest, err = FileInputDigest(ref, input)
	if err != nil {
		t.Fatal(err)
	}
	build.Files[0].AgentTaskID = "known-task"
	v, p := 3, 2
	task := &interfaces.BknAgentTask{TaskID: "known-task", AgentID: kb.DocumentExtractionAgentID, AgentVersion: &v, PromptVersion: &p, ModelID: "actual-model", Status: "succeeded", CreateTime: 1234}
	values := func() map[string]InstanceValue {
		return map[string]InstanceValue{"number": {Value: "ABC", Evidence: []InstanceCitation{{BlockID: "native-1", Quote: "ABC"}}}, "amount": {Value: json.Number("25.50"), Evidence: []InstanceCitation{{BlockID: "native-1", Quote: "25.50"}}}}
	}
	proposal := FileExtraction{Records: []InstanceRecord{{Reference: "current", ObjectTypeID: entity.ID, Anchor: InstanceCitation{BlockID: "native-1", Quote: text}, Values: values()}, {Reference: "file", ObjectTypeID: file.ID, Anchor: InstanceCitation{BlockID: "native-1", Quote: text}, Values: values()}}, Relations: []InstanceRelation{}}
	raw, _ := json.Marshal(proposal)
	task.Output = string(raw)
	return build, draft, input, task
}
func TestFileMaterializationKeepsIndependentClaimsAndOriginalWriteGateAcrossDomains(t *testing.T) {
	for _, domain := range []string{"contract", "patent", "inspection"} {
		for _, original := range []bool{false, true} {
			t.Run(domain+map[bool]string{false: "_location", true: "_original"}[original], func(t *testing.T) {
				build, draft, input, task := extractionFixture(t, domain, original)
				writes, err := MaterializeFile(build, 0, draft, input, task)
				if err != nil {
					t.Fatal(err)
				}
				fileRows, blockRows, claims := 0, 0, 0
				for _, write := range writes {
					if write.Spec.ObjectTypeID == domain+"_doc" {
						t.Fatal("one file overwrote a canonical current entity")
					}
					if write.Spec.ObjectTypeID == domain+"_file" {
						fileRows++
					}
					if o := instanceObject(draft.Items, write.Spec.ObjectTypeID); o != nil && o.Document.Role == "evidence_block" {
						blockRows++
					}
					if write.Row["property_name"] == "amount" && write.Row["claim_value"] != nil {
						claims++
					}
					if !original {
						if q, ok := write.Row["source_quote"]; ok && q != "" {
							t.Fatal("disabled original quote persisted")
						}
						raw, _ := json.Marshal(write.Row)
						if strings.Contains(string(raw), "Reference ABC") {
							t.Fatal("raw source leaked through nested evidence")
						}
					}
					if write.Row["model_fingerprint"] != build.ModelFingerprint || write.Row["extraction_version"] == "" {
						t.Fatal("row lacks actual execution/model provenance")
					}
				}
				if fileRows != 1 || claims < 2 || (original && blockRows != 1) || (!original && blockRows != 0) {
					t.Fatal("claims or original gate incomplete", fileRows, claims, blockRows)
				}
			})
		}
	}
}

func TestImageOnlyMaterializationPreservesNativeEvidenceWithoutInventingAnAgentExecution(t *testing.T) {
	for _, original := range []bool{false, true} {
		build, draft, _, _ := extractionFixture(t, "inspection", original)
		ref := fileRef(build.Files[0].File)
		input := &kb.FileInput{Text: map[string]string{}, Media: []kb.EvidenceMedia{{SampleVersion: ref, MediaID: "original-resource", OccurrenceID: "original-position", ImageRef: "original-position", Kind: "original", MimeType: "image/jpeg"}}}
		var err error
		build.Files[0].AgentTaskID = ""
		build.Files[0].InputDigest, err = FileInputDigest(ref, input)
		if err != nil {
			t.Fatal(err)
		}
		writes, err := MaterializeNativeFile(build, 0, draft, input)
		if err != nil {
			t.Fatal(err)
		}
		media := 0
		for _, write := range writes {
			if write.Row["extraction_version"] != NativeEvidenceVersion {
				t.Fatal("invented model execution")
			}
			if object := instanceObject(draft.Items, write.Spec.ObjectTypeID); object != nil {
				if !strings.HasPrefix(object.Document.Role, "evidence_") {
					t.Fatal("invented business instance")
				}
				if object.Document.Role == "evidence_media" {
					media++
				}
			}
		}
		if original && media != 1 || !original && media != 0 {
			t.Fatal("original-image write gate was not honored", original, media)
		}
	}
}
func TestMaterializationRejectsWholeInvalidFileBeforeReturningAnyRows(t *testing.T) {
	for _, scenario := range []string{"unknown_property", "wrong_type", "bad_quote", "unknown_object", "unconfirmed_number", "changed_bytes", "changed_model", "wrong_version", "wrong_task", "unknown_root", "unsupported_relation"} {
		t.Run(scenario, func(t *testing.T) {
			build, draft, input, task := extractionFixture(t, "contract", false)
			proposal, err := parseFileExtraction(task.Output)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "unknown_property":
				proposal.Records[0].Values["invented"] = InstanceValue{Value: "x"}
			case "wrong_type":
				v := proposal.Records[0].Values["amount"]
				v.Value = "25.50"
				proposal.Records[0].Values["amount"] = v
			case "bad_quote":
				v := proposal.Records[0].Values["number"]
				v.Evidence[0].Quote = "invented"
				proposal.Records[0].Values["number"] = v
			case "unknown_object":
				proposal.Records[0].ObjectTypeID = "unconfirmed"
			case "unconfirmed_number":
				draft.Items.ObjectTypes[1].Document.FileIdentity = nil
			case "changed_bytes":
				input.Text["native-1"] = "Reference XYZ. Total 25.50."
			case "changed_model":
				draft.Items.ObjectTypes[0].Description = "changed"
			case "wrong_version":
				v := 4
				task.AgentVersion = &v
			case "wrong_task":
				task.TaskID = "other-task"
			case "unsupported_relation":
				proposal.Relations = []InstanceRelation{{RelationTypeID: "invented", Source: "current", Target: "file"}}
			}
			raw, _ := json.Marshal(proposal)
			task.Output = string(raw)
			if scenario == "unknown_root" {
				task.Output = strings.TrimSuffix(task.Output, "}") + `,"other":true}`
			}
			if writes, err := MaterializeFile(build, 0, draft, input, task); err == nil || writes != nil {
				t.Fatal("invalid file returned partial/unverified rows", scenario)
			}
		})
	}
}
func TestRepeatedFileMaterializationIsIndependentOfAgentRecordOrder(t *testing.T) {
	build, draft, input, task := extractionFixture(t, "contract", true)
	first, err := MaterializeFile(build, 0, draft, input, task)
	if err != nil {
		t.Fatal(err)
	}
	proposal, _ := parseFileExtraction(task.Output)
	proposal.Records[0], proposal.Records[1] = proposal.Records[1], proposal.Records[0]
	raw, _ := json.Marshal(proposal)
	task.Output = string(raw)
	second, err := MaterializeFile(build, 0, draft, input, task)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	if string(a) != string(b) {
		t.Fatal("unchanged file produced unstable rows")
	}
}

func TestWholeFileSentenceRecordsKeepIDsWhenOrdinaryValuesAreCorrected(t *testing.T) {
	build, draft, input, task := extractionFixture(t, "inspection", false)
	local := &interfaces.DraftObjectType{ID: "inspection_line", Name: "Inspection line", Adopted: true, PrimaryKeys: []string{"_id"}, DisplayKey: "number", Document: &interfaces.DraftDocumentModel{Role: "in_document", Layer: "fact", SelectionID: build.SelectionID, SelectionRevision: build.SelectionRevision}}
	for _, field := range draft.Items.ObjectTypes[0].Properties {
		copy := *field
		local.Properties = append(local.Properties, &copy)
	}
	if err := kbconcept.ConfirmSentenceIdentity(local, interfaces.SentenceIdentityDecision{Properties: []string{"number"}, Confirm: true}, build.Owner, 10); err != nil {
		t.Fatal(err)
	}
	draft.Items.ObjectTypes = append(draft.Items.ObjectTypes, local)
	evidence, err := kbconcept.BuildEvidence(&kbconcept.SampleInput{SelectionID: build.SelectionID, SelectionRevision: build.SelectionRevision, Request: kb.ConceptRequest{NamingLanguage: "en-US"}}, draft.Items)
	if err != nil {
		t.Fatal(err)
	}
	for _, object := range evidence.Model.ObjectTypes {
		object.Adopted = true
		draft.Items.ObjectTypes = append(draft.Items.ObjectTypes, object)
	}
	for _, relation := range evidence.Model.RelationTypes {
		relation.Adopted = true
		draft.Items.RelationTypes = append(draft.Items.RelationTypes, relation)
	}
	draft.Items.DatasetPlans = nil
	plan, err := kbdataset.NewPlan(draft, "generation", "catalog", build.Owner)
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
	text := "Order A amount 100; order B amount 200."
	input.Blocks[0].End = utf8.RuneCountInString(text)
	input.Text["native-1"] = text
	build.Files[0].InputDigest, err = FileInputDigest(fileRef(build.Files[0].File), input)
	if err != nil {
		t.Fatal(err)
	}
	proposal := FileExtraction{Records: []InstanceRecord{}, Relations: []InstanceRelation{}}
	for i, number := range []string{"A", "B"} {
		amount := []json.Number{"200", "100"}[i]
		proposal.Records = append(proposal.Records, InstanceRecord{Reference: number, ObjectTypeID: local.ID, Anchor: InstanceCitation{BlockID: "native-1", Quote: text}, Values: map[string]InstanceValue{"number": {Value: number, Evidence: []InstanceCitation{{BlockID: "native-1", Quote: []string{"Order A", "order B"}[i]}}}, "amount": {Value: amount, Evidence: []InstanceCitation{{BlockID: "native-1", Quote: amount.String()}}}}})
	}
	extract := func() map[string]string {
		raw, _ := json.Marshal(proposal)
		task.Output = string(raw)
		writes, err := MaterializeFile(build, 0, draft, input, task)
		if err != nil {
			t.Fatal(err)
		}
		ids := map[string]string{}
		for _, write := range writes {
			if write.Spec.ObjectTypeID == local.ID {
				ids[write.Row["number"].(string)] = write.Row["_id"].(string)
			}
		}
		return ids
	}
	before := extract()
	for i := range proposal.Records {
		value := proposal.Records[i].Values["amount"]
		value.Value = []json.Number{"100", "200"}[i]
		value.Evidence[0].Quote = value.Value.(json.Number).String()
		proposal.Records[i].Values["amount"] = value
	}
	after := extract()
	if len(before) != 2 || before["A"] != after["A"] || before["B"] != after["B"] {
		t.Fatal("correcting extracted values swapped source object identities", before, after)
	}
}
