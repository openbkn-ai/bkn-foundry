// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbdataset

import (
	"fmt"
	"unicode"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/locale"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
)

// sourceColumns are what a source record declares of its link Dataset row: which property value
// one file gave for the fused object, and where in that file.
var sourceColumns = []string{"property_name", "claim_value", "document_id", "content_version_id", "block_id", "source_start", "source_end", "source_quote", "evidence"}

type publishedSources struct {
	objects   []*interfaces.DraftObjectType
	relations []*interfaces.DraftRelationType
}

// sourceRecords publishes, for each fused object, the rows of its existing evidence link to the
// source document as an object type of their own: one row per value a file gave for one of its
// properties. It binds that same Dataset; nothing is written or copied. The object reaches its
// records by its record id, and each record reaches its file, and its block when originals are
// included.
func sourceRecords(items *interfaces.DraftItems, objects map[string]*interfaces.DraftObjectType, aliases, roles map[string]string, adopted func(string) bool) (*publishedSources, error) {
	result := &publishedSources{}
	document, block := roles["evidence_document"], roles["evidence_block"]
	if !adopted(document) {
		return result, nil
	}
	used := map[string]bool{}
	for _, object := range items.ObjectTypes {
		if object != nil {
			used[object.ID] = true
		}
	}
	for _, relation := range items.RelationTypes {
		if relation != nil {
			used[relation.ID] = true
		}
	}
	language := ""
	if items.Inputs != nil {
		language = items.Inputs.NamingLanguage
	}
	for _, entity := range items.ObjectTypes {
		if entity == nil || !entity.Adopted || entity.Document == nil || entity.Document.Role != "entity" {
			continue
		}
		var link *interfaces.DraftRelationType
		for _, relation := range items.RelationTypes {
			if relation != nil && relation.Adopted && relation.Document != nil && relation.Document.Role == "evidence_reference" && relation.Source == entity.ID && relation.Target == document {
				link = relation
				break
			}
		}
		if link == nil {
			continue
		}
		if link.LinkTable == nil || link.LinkTable.ResourceID == "" || aliases[entity.ID] == "" || aliases[document] == "" {
			return nil, fmt.Errorf("fused object %q has no prepared source records", entity.ID)
		}
		columns := map[string]*interfaces.DraftProperty{}
		for _, property := range link.Properties {
			if property != nil {
				columns[property.Name] = property
			}
		}
		lang := language
		if lang == "" {
			lang = nameLanguage(entity.Name)
		}
		field := func(name, typ, mapped string) *interfaces.DraftProperty {
			label := sourceLabel(lang, "BknProject.Draft.DocumentField."+name, nil, name)
			return &interfaces.DraftProperty{Name: name, DisplayName: label, Type: typ, MappedField: mapped, ReadOnly: true, SourceOfRecord: "document"}
		}
		record := &interfaces.DraftObjectType{
			ID:          freeID(entity.ID+"_source", used),
			Name:        sourceLabel(lang, "BknProject.Draft.Evidence.source_records", map[string]any{"name": entity.Name}, entity.Name+" source records"),
			Adopted:     true,
			Table:       *link.LinkTable,
			PrimaryKeys: []string{"bkn_record_id"},
			DisplayKey:  "property_name",
			Key:         interfaces.DraftKeyEvidence{Source: "document_template"},
			Document:    &interfaces.DraftDocumentModel{Role: "evidence_source", Layer: "evidence", Citations: []interfaces.DraftSourceCitation{}},
			Properties:  []*interfaces.DraftProperty{field("bkn_record_id", "string", "bkn_record_id"), field("source_record_id", "string", "source_record_id")},
		}
		for _, name := range sourceColumns {
			column := columns[name]
			if column == nil || column.MappedField == "" {
				return nil, fmt.Errorf("fused object %q has no source column %q", entity.ID, name)
			}
			property := field(name, column.Type, column.MappedField)
			record.Properties = append(record.Properties, property)
		}
		relation := func(prefix, key, target string, cardinality string, mapping []interfaces.DraftFieldMapping) *interfaces.DraftRelationType {
			return &interfaces.DraftRelationType{
				ID: freeID(prefix, used), Name: sourceLabel(lang, "BknProject.Draft.Evidence."+key, map[string]any{"name": entity.Name}, key),
				Adopted: true, Source: record.ID, Target: target, Cardinality: cardinality, Origin: "document_template", Verification: "confirmed",
				Mapping: mapping, Document: &interfaces.DraftDocumentModel{Role: "evidence_source", Layer: "evidence", Citations: []interfaces.DraftSourceCitation{}},
				DocumentMapping: &interfaces.DraftDocumentMapping{Mode: "evidence"},
			}
		}
		owner := relation(entity.ID+"_sources", "has_source_records", record.ID, interfaces.DRAFT_CARDINALITY_ONE_TO_MANY, []interfaces.DraftFieldMapping{{SourceProperty: aliases[entity.ID], TargetProperty: "source_record_id"}})
		owner.Source = entity.ID
		result.objects = append(result.objects, record)
		result.relations = append(result.relations, owner,
			relation(record.ID+"_document", "source_record_document", document, interfaces.DRAFT_CARDINALITY_MANY_TO_ONE, []interfaces.DraftFieldMapping{{SourceProperty: "document_id", TargetProperty: aliases[document]}}))
		if adopted(block) && objects[block] != nil {
			result.relations = append(result.relations, relation(record.ID+"_block", "source_record_block", block, interfaces.DRAFT_CARDINALITY_MANY_TO_ONE, []interfaces.DraftFieldMapping{
				{SourceProperty: "document_id", TargetProperty: "document_id"}, {SourceProperty: "content_version_id", TargetProperty: "content_version_id"}, {SourceProperty: "block_id", TargetProperty: "block_id"},
			}))
		}
	}
	return result, nil
}

func sourceLabel(language, key string, values map[string]any, fallback string) string {
	locale.Register()
	text, err := i18n.Render(i18n.Language(language), key, values)
	if err != nil || text == "" {
		text = fallback
	}
	if utf8.RuneCountInString(text) > 128 {
		text = string([]rune(text)[:128])
	}
	return text
}

// nameLanguage is the language an item was named in, for drafts that did not record it.
func nameLanguage(name string) string {
	for _, r := range name {
		if unicode.Is(unicode.Han, r) {
			return "zh-CN"
		}
	}
	return "en-US"
}

// freeID is prefix, or prefix with the first free number, within what BKN accepts as an id.
func freeID(prefix string, used map[string]bool) string {
	if len(prefix) > 120 {
		prefix = prefix[:120]
	}
	id := prefix
	for n := 2; used[id]; n++ {
		id = fmt.Sprintf("%s_%d", prefix, n)
	}
	used[id] = true
	return id
}
