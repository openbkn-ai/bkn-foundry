// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbconcept

import (
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/locale"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
)

// EvidenceSet contains only missing template candidates. Existing matching
// templates retain their identities and human decisions on repeated extraction.
type EvidenceSet struct {
	Model      ValidatedModel
	DocumentID string
	BlockID    string
	ImageID    string
	MediaID    string
}

func evidenceLabel(language, key, fallback string) string {
	locale.Register()
	text, err := i18n.Render(i18n.Language(language), "BknProject.Draft.Evidence."+key, nil)
	if err != nil {
		return fallback
	}
	return text
}
func freeModelID(prefix string, used map[string]bool) string {
	id := prefix
	for n := 2; used[id]; n++ {
		id = prefix + "_" + strconv.Itoa(n)
	}
	used[id] = true
	return id
}
func schemaMatches(object *interfaces.DraftObjectType, fields []managedField) bool {
	if object.Document == nil || object.Document.Layer != "evidence" || len(object.PrimaryKeys) != 1 || object.PrimaryKeys[0] != "_id" || len(object.Properties) > len(fields) {
		return false
	}
	expected := map[string]bool{}
	for _, field := range fields {
		expected[field.Name] = true
	}
	seen := map[string]bool{}
	for _, p := range object.Properties {
		if p == nil || !expected[p.Name] || seen[p.Name] {
			return false
		}
		seen[p.Name] = true
	}
	for _, field := range fields {
		p := property(object, field.Name)
		if p == nil && (field.Name == "model_fingerprint" || field.Name == "extraction_version") {
			continue
		}
		if p == nil || p.Type != field.Type || !p.ReadOnly || p.Extraction != nil || p.SourceOfRecord != "document" {
			return false
		}
		if p.MappedField != "" {
			expected := field.Name
			if field.Name == "_id" {
				expected = "bkn_record_id"
			}
			if object.Table.ResourceID == "" || p.MappedField != expected {
				return false
			}
		}
	}
	return true
}

// BuildEvidence generates model definitions only. Block/image rows remain gated
// by each source's include_original decision in the later instance writer; the
// quote column always exists, even when the source permits location-only data.
func BuildEvidence(input *SampleInput, existing interfaces.DraftItems) (*EvidenceSet, error) {
	if input == nil || input.SelectionID == "" || input.SelectionRevision < 1 {
		return nil, ErrInvalidModel
	}
	result := &EvidenceSet{Model: ValidatedModel{ObjectTypes: []*interfaces.DraftObjectType{}, RelationTypes: []*interfaces.DraftRelationType{}}}
	used := map[string]bool{}
	for _, o := range existing.ObjectTypes {
		if o != nil {
			used[o.ID] = true
		}
	}
	for _, r := range existing.RelationTypes {
		if r != nil {
			used[r.ID] = true
		}
	}
	templates := []struct {
		role, prefix, label, fallback string
		fields                        []managedField
		id                            *string
	}{
		{"evidence_document", "source_document", "document", "Source document", []managedField{{"_id", "string", "Record identifier"}, {"knowledge_base_id", "string", "Knowledge base"}, {"file_id", "string", "File"}, {"content_version_id", "string", "Content version"}, {"name", "string", "File name"}}, &result.DocumentID},
		{"evidence_block", "source_block", "block", "Source block", []managedField{{"_id", "string", "Record identifier"}, {"document_id", "string", "Document"}, {"content_version_id", "string", "Content version"}, {"block_id", "string", "Source block"}, {"source_start", "integer", "Quote start"}, {"source_end", "integer", "Quote end"}, {"source_quote", "text", "Source quote"}}, &result.BlockID},
		{"evidence_media", "source_media", "media", "Image resource", []managedField{{"_id", "string", "Record identifier"}, {"document_id", "string", "Document"}, {"content_version_id", "string", "Content version"}, {"media_id", "string", "Image resource"}, {"mime_type", "string", "Media type"}}, &result.MediaID},
		{"evidence_image", "source_image", "image", "Source image", []managedField{{"_id", "string", "Record identifier"}, {"document_id", "string", "Document"}, {"content_version_id", "string", "Content version"}, {"block_id", "string", "Source block"}, {"image_ref", "string", "Image reference"}}, &result.ImageID},
	}
	language := input.Request.NamingLanguage
	for _, template := range templates {
		template.fields = append(template.fields, managedField{"model_fingerprint", "string", "Confirmed model version"}, managedField{"extraction_version", "string", "Extraction version"})
		for _, existing := range existing.ObjectTypes {
			if existing == nil || existing.Document == nil || existing.Document.Role != template.role {
				continue
			}
			if *template.id != "" || !schemaMatches(existing, template.fields) {
				return nil, ErrInvalidModel
			}
			*template.id = existing.ID
		}
		if *template.id != "" {
			continue
		}
		*template.id = freeModelID(template.prefix, used)
		object := &interfaces.DraftObjectType{ID: *template.id, Name: evidenceLabel(language, template.label, template.fallback), PrimaryKeys: []string{"_id"}, DisplayKey: "_id", Key: interfaces.DraftKeyEvidence{Source: "document_template"}, Document: &interfaces.DraftDocumentModel{Role: template.role, Layer: "evidence", Reason: evidenceLabel(language, "reason", "Locate the original evidence"), SelectionID: input.SelectionID, SelectionRevision: input.SelectionRevision, Citations: []interfaces.DraftSourceCitation{}}}
		for _, f := range template.fields {
			label, err := i18n.Render(i18n.Language(language), "BknProject.Draft.DocumentField."+f.Name, nil)
			if err != nil {
				label = f.Label
			}
			object.Properties = append(object.Properties, &interfaces.DraftProperty{Name: f.Name, DisplayName: label, Type: f.Type, ReadOnly: true, SourceOfRecord: "document"})
		}
		if template.role == "evidence_document" {
			object.DisplayKey = "name"
		}
		result.Model.ObjectTypes = append(result.Model.ObjectTypes, object)
	}
	for _, child := range []struct{ id, prefix, label, fallback string }{{result.BlockID, "source_document_has_blocks", "contains_blocks", "Contains source blocks"}, {result.ImageID, "source_document_has_images", "contains_images", "Contains source images"}, {result.MediaID, "source_document_has_media", "contains_media", "Contains image resources"}} {
		found := false
		for _, r := range existing.RelationTypes {
			if r == nil || r.Document == nil || r.Document.Role != "evidence_contains" || r.Source != result.DocumentID || r.Target != child.id {
				continue
			}
			if found || r.Cardinality != interfaces.DRAFT_CARDINALITY_ONE_TO_MANY || len(r.Mapping) != 1 || r.Mapping[0].SourceProperty != "_id" || r.Mapping[0].TargetProperty != "document_id" || r.DocumentMapping == nil || r.DocumentMapping.Mode != "evidence" {
				return nil, ErrInvalidModel
			}
			found = true
		}
		if found {
			continue
		}
		result.Model.RelationTypes = append(result.Model.RelationTypes, &interfaces.DraftRelationType{ID: freeModelID(child.prefix, used), Name: evidenceLabel(language, child.label, child.fallback), Source: result.DocumentID, Target: child.id, Cardinality: interfaces.DRAFT_CARDINALITY_ONE_TO_MANY, Origin: "document_template", Verification: "confirmed", Mapping: []interfaces.DraftFieldMapping{{SourceProperty: "_id", TargetProperty: "document_id"}}, Document: &interfaces.DraftDocumentModel{Role: "evidence_contains", Layer: "evidence", Reason: evidenceLabel(language, "reason", "Locate the original evidence"), SelectionID: input.SelectionID, SelectionRevision: input.SelectionRevision, Citations: []interfaces.DraftSourceCitation{}}, DocumentMapping: &interfaces.DraftDocumentMapping{Mode: "evidence"}})
	}
	if err := buildEvidenceReferences(input, existing, result, used); err != nil {
		return nil, err
	}
	return result, nil
}

func buildEvidenceReferences(input *SampleInput, existing interfaces.DraftItems, result *EvidenceSet, used map[string]bool) error {
	for _, source := range existing.ObjectTypes {
		if source == nil || source.Document == nil || strings.HasPrefix(source.Document.Role, "evidence_") {
			continue
		}
		if _, ok := roleLayers[source.Document.Role]; !ok || source.Document.Role == "relation" {
			return ErrInvalidModel
		}
		for _, target := range []struct{ id, label, fallback string }{
			{result.DocumentID, "reference_document", "Source document"},
			{result.BlockID, "reference_block", "Source block"},
			{result.ImageID, "reference_image", "Image occurrence"},
		} {
			// Preserve previously confirmed direct references. New provenance edges
			// are many-to-many so attributes can cite several documents/blocks.
			found := false
			for _, r := range existing.RelationTypes {
				if r == nil || r.Document == nil || r.Document.Role != "evidence_reference" || r.Source != source.ID || r.Target != target.id {
					continue
				}
				if found || r.DocumentMapping == nil || r.DocumentMapping.Mode != "evidence" || !validEvidenceReference(source, r, result) {
					return ErrInvalidModel
				}
				found = true
			}
			if found {
				continue
			}
			digest := sha256.Sum256([]byte(source.ID + "/" + target.id))
			prefix := source.ID
			if len(prefix) > 16 {
				prefix = prefix[:16]
			}
			id := freeModelID(fmt.Sprintf("%s_evidence_%x", prefix, digest[:4]), used)
			label, err := i18n.Render(i18n.Language(input.Request.NamingLanguage), "BknProject.Draft.Evidence."+target.label, map[string]any{"name": source.Name})
			if err != nil {
				label = source.Name + " " + target.fallback
			}
			if utf8.RuneCountInString(label) > 128 {
				label = string([]rune(label)[:128])
			}
			result.Model.RelationTypes = append(result.Model.RelationTypes, evidenceRelation(input, id, label, source.ID, target.id, "evidence_reference", source.Document.Layer))
		}
	}
	// A resource can occur repeatedly and one occurrence can span several blocks.
	for _, edge := range []struct{ source, target, role, prefix, label, fallback string }{
		{result.MediaID, result.ImageID, "evidence_occurrence", "source_media_occurrences", "occurrences", "Image occurrences"},
		{result.ImageID, result.BlockID, "evidence_occurrence", "source_image_blocks", "image_blocks", "Image source blocks"},
	} {
		found := false
		for _, r := range existing.RelationTypes {
			if r == nil || r.Document == nil || r.Document.Role != edge.role || r.Source != edge.source || r.Target != edge.target {
				continue
			}
			if found || r.Cardinality != interfaces.DRAFT_CARDINALITY_MANY_TO_MANY || len(r.Mapping) != 0 || r.DocumentMapping == nil || r.DocumentMapping.Mode != "evidence" {
				return ErrInvalidModel
			}
			found = true
		}
		if !found {
			result.Model.RelationTypes = append(result.Model.RelationTypes, evidenceRelation(input, freeModelID(edge.prefix, used), evidenceLabel(input.Request.NamingLanguage, edge.label, edge.fallback), edge.source, edge.target, edge.role, "evidence"))
		}
	}
	return nil
}

func evidenceRelation(input *SampleInput, id, name, source, target, role, layer string) *interfaces.DraftRelationType {
	return &interfaces.DraftRelationType{ID: id, Name: name, Source: source, Target: target, Cardinality: interfaces.DRAFT_CARDINALITY_MANY_TO_MANY, Origin: "document_template", Verification: "confirmed", Document: &interfaces.DraftDocumentModel{Role: role, Layer: layer, Reason: evidenceLabel(input.Request.NamingLanguage, "reason", "Locate the original evidence"), SelectionID: input.SelectionID, SelectionRevision: input.SelectionRevision, Citations: []interfaces.DraftSourceCitation{}}, DocumentMapping: &interfaces.DraftDocumentMapping{Mode: "evidence"}}
}

func validEvidenceReference(source *interfaces.DraftObjectType, r *interfaces.DraftRelationType, result *EvidenceSet) bool {
	if r.Cardinality == interfaces.DRAFT_CARDINALITY_MANY_TO_MANY {
		return len(r.Mapping) == 0
	}
	if source.Document.Role == "entity" || r.Cardinality != interfaces.DRAFT_CARDINALITY_MANY_TO_ONE {
		return false
	}
	var expected []interfaces.DraftFieldMapping
	switch r.Target {
	case result.DocumentID:
		expected = []interfaces.DraftFieldMapping{{SourceProperty: "document_id", TargetProperty: "_id"}}
	case result.BlockID:
		expected = []interfaces.DraftFieldMapping{{SourceProperty: "document_id", TargetProperty: "document_id"}, {SourceProperty: "content_version_id", TargetProperty: "content_version_id"}, {SourceProperty: "block_id", TargetProperty: "block_id"}}
	default:
		return false
	}
	if len(r.Mapping) != len(expected) {
		return false
	}
	for i, mapping := range expected {
		if r.Mapping[i] != mapping || property(source, mapping.SourceProperty) == nil {
			return false
		}
	}
	return true
}
