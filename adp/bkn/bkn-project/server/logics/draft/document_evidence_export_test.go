// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/bkn-specification/bkn"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbdataset"
)

var technicalFields = []string{"entity_id", "document_id", "content_version_id", "block_id", "source_start", "source_end", "source_quote", "extraction_version", "model_fingerprint"}

type evidenceDomain struct {
	name, language                    string
	fused, fusedName, fusedKey        string
	file, fileName, fileField, factID string
}

// evidenceDraft is a document model as concept extraction leaves it: a fused business object, an
// object per file and a fact, each with the technical columns every build writes, and the evidence
// templates with their references.
func evidenceDraft(domain evidenceDomain) *interfaces.Draft {
	doc := func(role string) *interfaces.DraftDocumentModel {
		return &interfaces.DraftDocumentModel{Role: role, SelectionID: "selection", SelectionRevision: 1}
	}
	ro := func(name, typ string) *interfaces.DraftProperty {
		return &interfaces.DraftProperty{Name: name, DisplayName: name, Type: typ, ReadOnly: true, SourceOfRecord: "document"}
	}
	business := func(id, role string, fields ...string) *interfaces.DraftObjectType {
		object := &interfaces.DraftObjectType{ID: id, Name: id, Adopted: true, Document: doc(role), PrimaryKeys: []string{"_id"}, DisplayKey: fields[0]}
		object.Properties = append(object.Properties, ro("_id", "string"))
		for _, field := range fields {
			object.Properties = append(object.Properties, &interfaces.DraftProperty{Name: field, DisplayName: field, Type: "string", Extraction: &interfaces.DraftExtractionField{}})
		}
		for _, field := range technicalFields {
			typ := "string"
			if field == "source_start" || field == "source_end" {
				typ = "integer"
			} else if field == "source_quote" {
				typ = "text"
			}
			object.Properties = append(object.Properties, ro(field, typ))
		}
		return object
	}
	evidence := func(id, role string, fields ...string) *interfaces.DraftObjectType {
		object := &interfaces.DraftObjectType{ID: id, Name: id, Adopted: true, Document: doc(role), PrimaryKeys: []string{"_id"}, DisplayKey: "_id"}
		for _, field := range append(append([]string{"_id"}, fields...), "model_fingerprint", "extraction_version") {
			typ := "string"
			if field == "source_start" || field == "source_end" {
				typ = "integer"
			}
			object.Properties = append(object.Properties, ro(field, typ))
		}
		return object
	}
	fused := business(domain.fused, "entity", domain.fusedKey)
	fused.Name, fused.PrimaryKeys = domain.fusedName, []string{domain.fusedKey}
	file := business(domain.file, "file", domain.fileField)
	file.Name = domain.fileName
	fact := business(domain.factID, "fact", "status_text")
	objects := []*interfaces.DraftObjectType{
		evidence("source_document", "evidence_document", "knowledge_base_id", "file_id", "content_version_id", "name"),
		evidence("source_block", "evidence_block", "document_id", "content_version_id", "block_id", "source_start", "source_end", "source_quote"),
		evidence("source_image", "evidence_image", "document_id", "content_version_id", "block_id", "image_ref"),
		evidence("source_media", "evidence_media", "document_id", "content_version_id", "media_id", "mime_type"),
		fused, file, fact,
	}
	relation := func(id, role, source, target, cardinality string, mapping ...interfaces.DraftFieldMapping) *interfaces.DraftRelationType {
		return &interfaces.DraftRelationType{ID: id, Name: id, Adopted: true, Source: source, Target: target, Cardinality: cardinality, Document: doc(role), DocumentMapping: &interfaces.DraftDocumentMapping{Mode: "evidence"}, Mapping: mapping}
	}
	relations := []*interfaces.DraftRelationType{
		relation("source_document_has_blocks", "evidence_contains", "source_document", "source_block", "one_to_many", interfaces.DraftFieldMapping{SourceProperty: "_id", TargetProperty: "document_id"}),
		relation("source_document_has_images", "evidence_contains", "source_document", "source_image", "one_to_many", interfaces.DraftFieldMapping{SourceProperty: "_id", TargetProperty: "document_id"}),
		relation("source_image_blocks", "evidence_occurrence", "source_image", "source_block", "many_to_many"),
	}
	for _, source := range []string{domain.fused, domain.file, domain.factID} {
		for _, target := range []string{"source_document", "source_block", "source_image"} {
			relations = append(relations, relation(source+"_to_"+target, "evidence_reference", source, target, "many_to_many"))
		}
	}
	return &interfaces.Draft{ProjectID: "p", Items: interfaces.DraftItems{ObjectTypes: objects, RelationTypes: relations, Inputs: &interfaces.DraftInputs{NamingLanguage: domain.language}}}
}

func boundEvidenceDraft(t *testing.T, domain evidenceDomain) *interfaces.Draft {
	t.Helper()
	d := evidenceDraft(domain)
	plan, err := kbdataset.NewPlan(d, "generation", "catalog", interfaces.AccountInfo{ID: "owner", Type: "user"})
	if err != nil {
		t.Fatal(err)
	}
	for i := range plan.Datasets {
		plan.Datasets[i].Ready = true
	}
	bound, err := kbdataset.BindPreview(d, plan)
	if err != nil {
		t.Fatal(err)
	}
	return bound
}

func compiledNetwork(t *testing.T, d *interfaces.Draft, evidence *interfaces.DraftEvidenceExport) *bkn.BknNetwork {
	t.Helper()
	compiled, err := compileDraft(&d.Items, evidence)
	if err != nil {
		t.Fatal(err)
	}
	network, err := loadWithBranch(compiled.Files)
	if err != nil {
		t.Fatal(err)
	}
	if err := bkn.ValidateNetworkDeclarations(network); err != nil {
		t.Fatal(err)
	}
	if validation := bkn.ValidateNetwork(network); !validation.OK() {
		t.Fatalf("BKN semantic validation failed: %+v", validation.Errors)
	}
	return network
}

func declaredNames(object *bkn.BknObjectType) []string {
	var names []string
	for _, p := range object.DataProperties {
		names = append(names, p.Name)
	}
	return names
}

var evidenceDomains = []evidenceDomain{
	{name: "procurement", language: "en-US", fused: "supplier_doc", fusedName: "Supplier", fusedKey: "supplier_name", file: "invoice_doc", fileName: "Invoice", fileField: "invoice_no", factID: "payment_fact"},
	{name: "patents", language: "zh-CN", fused: "applicant_doc", fusedName: "申请人", fusedKey: "applicant_name", file: "patent_doc", fileName: "专利", fileField: "patent_no", factID: "legal_status_fact"},
}

func TestPublishedDocumentNetworkKeepsBusinessFieldsAndSourceRecords(t *testing.T) {
	for _, domain := range evidenceDomains {
		for _, originals := range []bool{false, true} {
			t.Run(domain.name+map[bool]string{false: "/no_originals", true: "/originals"}[originals], func(t *testing.T) {
				d := boundEvidenceDraft(t, domain)
				before, _ := json.Marshal(d)
				network := compiledNetwork(t, d, &interfaces.DraftEvidenceExport{IncludeOriginal: originals})
				if after, _ := json.Marshal(d); string(after) != string(before) {
					t.Fatal("publishing changed the draft")
				}
				objects := map[string]*bkn.BknObjectType{}
				for _, o := range network.ObjectTypes {
					objects[o.ID] = o
				}
				relations := map[string]*bkn.BknRelationType{}
				for _, r := range network.RelationTypes {
					relations[r.ID] = r
					if r.Endpoint.Target == "source_image" && r.Endpoint.Source != "source_document" {
						t.Fatalf("relation %s references images, which are never written", r.ID)
					}
					if r.Endpoint.Source == domain.fused && r.Endpoint.Type == "indirect" {
						t.Fatalf("fused object kept its many-to-many evidence relation %s", r.ID)
					}
				}
				for _, id := range []string{domain.fused, domain.file, domain.factID} {
					for _, name := range technicalFields {
						if slices.Contains(declaredNames(objects[id]), name) {
							t.Fatalf("%s declares technical column %s", id, name)
						}
					}
				}
				if !slices.Contains(declaredNames(objects[domain.fused]), "bkn_record_id") || !slices.Contains(declaredNames(objects[domain.fused]), domain.fusedKey) {
					t.Fatalf("fused object lost its key or record id: %v", declaredNames(objects[domain.fused]))
				}
				if !slices.Contains(declaredNames(objects[domain.file]), domain.fileField) {
					t.Fatal("business field hidden")
				}
				for _, id := range []string{"source_document", "source_block"} {
					if objects[id] != nil && (slices.Contains(declaredNames(objects[id]), "model_fingerprint") || slices.Contains(declaredNames(objects[id]), "extraction_version")) {
						t.Fatalf("%s declares build columns", id)
					}
				}
				for _, id := range []string{"source_block", "source_image", "source_media"} {
					if (objects[id] != nil) != originals {
						t.Fatalf("original type %s published=%v with originals=%v", id, objects[id] != nil, originals)
					}
				}
				if objects["source_block"] != nil && !slices.Contains(declaredNames(objects["source_block"]), "source_quote") {
					t.Fatal("block lost its quote")
				}
				// Non-fused objects keep their many-to-many file evidence.
				if relations[domain.file+"_to_source_document"] == nil || relations[domain.factID+"_to_source_document"] == nil {
					t.Fatal("non-fused evidence relation dropped")
				}
				if (relations[domain.file+"_to_source_block"] != nil) != originals {
					t.Fatal("block evidence relation not gated by originals")
				}

				source := objects[domain.fused+"_source"]
				if source == nil {
					t.Fatalf("no source records for %s", domain.fused)
				}
				var link *interfaces.DraftRelationType
				for _, r := range d.Items.RelationTypes {
					if r.ID == domain.fused+"_to_source_document" {
						link = r
					}
				}
				if source.DataSource == nil || source.DataSource.ID != link.LinkTable.ResourceID {
					t.Fatal("source records do not bind the existing evidence Dataset")
				}
				for _, name := range []string{"bkn_record_id", "source_record_id", "property_name", "claim_value", "document_id", "block_id", "source_quote", "evidence"} {
					if !slices.Contains(declaredNames(source), name) {
						t.Fatalf("source records lack %s: %v", name, declaredNames(source))
					}
				}
				if !slices.Equal(source.PrimaryKeys, []string{"bkn_record_id"}) {
					t.Fatal("source records keyed otherwise")
				}
				owner := relations[domain.fused+"_sources"]
				if owner == nil || owner.Endpoint.Source != domain.fused || owner.Endpoint.Target != source.ID || owner.Endpoint.Type != "direct" {
					t.Fatal("fused object does not reach its source records")
				}
				if rules := owner.MappingRules.(bkn.DirectMappingRule); rules[0].SourceProperty != "bkn_record_id" || rules[0].TargetProperty != "source_record_id" {
					t.Fatalf("owner mapping %+v", rules)
				}
				file := relations[source.ID+"_document"]
				if file == nil || file.Endpoint.Target != "source_document" {
					t.Fatal("source records do not reach their file")
				}
				if rules := file.MappingRules.(bkn.DirectMappingRule); rules[0].SourceProperty != "document_id" || rules[0].TargetProperty != "bkn_record_id" {
					t.Fatalf("file mapping %+v", rules)
				}
				if (relations[source.ID+"_block"] != nil) != originals {
					t.Fatal("source record block relation not gated by originals")
				}
				if domain.language == "zh-CN" && source.Name != domain.fusedName+"的来源记录" {
					t.Fatalf("source records named %q", source.Name)
				}
			})
		}
	}
}

func TestEarlierConfirmationsKeepTheirProjection(t *testing.T) {
	d := boundEvidenceDraft(t, evidenceDomains[0])
	network := compiledNetwork(t, d, nil)
	for _, o := range network.ObjectTypes {
		if o.ID == "supplier_doc_source" {
			t.Fatal("source records added to an earlier projection")
		}
		if o.ID == "supplier_doc" && !slices.Contains(declaredNames(o), "document_id") {
			t.Fatal("earlier projection changed")
		}
	}
}

func TestConfirmingPublishesOriginalsByTheCurrentSelection(t *testing.T) {
	d := evidenceDraft(evidenceDomains[0])
	for _, c := range []struct {
		name      string
		selection *interfaces.KBSelection
		want      bool
	}{
		{"none", &interfaces.KBSelection{Ranges: []interfaces.KBSourceRange{{KnowledgeBaseID: "1"}}, Files: []interfaces.KBResourceFile{{FileID: "a"}}}, false},
		{"range", &interfaces.KBSelection{Ranges: []interfaces.KBSourceRange{{KnowledgeBaseID: "1"}, {KnowledgeBaseID: "2", IncludeOriginal: true}}}, true},
		{"file", &interfaces.KBSelection{Files: []interfaces.KBResourceFile{{FileID: "a"}, {FileID: "b", IncludeOriginal: true}}}, true},
	} {
		service := &draftService{kbResources: &datasetSelectionAccess{state: &interfaces.KBResourceState{Current: c.selection}}}
		evidence, err := service.evidenceExport(context.Background(), d)
		if err != nil || evidence == nil || evidence.IncludeOriginal != c.want {
			t.Fatalf("%s: %+v %v", c.name, evidence, err)
		}
	}
	tables := &interfaces.Draft{Items: interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{{ID: "t", Adopted: true}}}}
	if evidence, err := (&draftService{}).evidenceExport(context.Background(), tables); evidence != nil || err != nil {
		t.Fatal("a table-only draft got a document projection")
	}
}

// What reads the projection, such as the published bindings check, must expect only what the
// compiler wrote: no relation with an end left out.
func TestProjectionLeavesOutRelationsOfTypesNotPublished(t *testing.T) {
	for _, originals := range []bool{false, true} {
		d := boundEvidenceDraft(t, evidenceDomains[0])
		projected, err := kbdataset.ExportItems(&d.Items, &interfaces.DraftEvidenceExport{IncludeOriginal: originals})
		if err != nil {
			t.Fatal(err)
		}
		network := compiledNetwork(t, d, &interfaces.DraftEvidenceExport{IncludeOriginal: originals})
		compiled := map[string]bool{}
		for _, r := range network.RelationTypes {
			compiled[r.ID] = true
		}
		adopted := 0
		for _, r := range projected.RelationTypes {
			if r.Adopted {
				adopted++
				if !compiled[r.ID] {
					t.Fatalf("originals=%v: projection keeps %s, which is not compiled", originals, r.ID)
				}
			}
		}
		if adopted != len(network.RelationTypes) {
			t.Fatalf("originals=%v: %d adopted, %d compiled", originals, adopted, len(network.RelationTypes))
		}
	}
}
