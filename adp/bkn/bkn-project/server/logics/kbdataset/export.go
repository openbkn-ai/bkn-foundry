// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbdataset

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// ExportOptions say how the document evidence of a draft is published. Nil is the projection of
// confirmations made before business objects left their technical columns out: networks already
// published that way keep being read and checked as they were.
type ExportOptions = interfaces.DraftEvidenceExport

// technicalColumns are written on every business row for building and refreshing, and read from
// the Dataset; agents and people querying the network have no use for them.
var technicalColumns = map[string]bool{
	"entity_id": true, "document_id": true, "content_version_id": true, "block_id": true,
	"source_start": true, "source_end": true, "source_quote": true,
	"extraction_version": true, "model_fingerprint": true,
}

// buildColumns are the build's bookkeeping on evidence rows.
var buildColumns = map[string]bool{"extraction_version": true, "model_fingerprint": true}

// originalRoles are the evidence types holding the original text and images of a file: they have
// rows only when a file of the selection includes its original.
var originalRoles = map[string]bool{"evidence_block": true, "evidence_image": true, "evidence_media": true}

// HiddenColumn tells whether a published document object of role leaves property out of its
// definition. Its Dataset column stays, for the build and refresh.
func HiddenColumn(role, property string) bool {
	switch role {
	case "entity", "file", "in_document", "fact", "event":
		return technicalColumns[property]
	case "evidence_document", "evidence_block", "evidence_image", "evidence_media":
		return buildColumns[property]
	}
	return false
}

// ExportItems is the compiler projection of a draft's document model to BKN. It translates the
// managed write identity to its prepared ordinary Dataset field, since BKN property names cannot
// begin with '_'. With options it also publishes only what agents can use: business objects
// without their technical columns, the source records of each fused object instead of its
// many-to-many evidence relations, and the original text and image types only when a file of the
// selection includes its original. Logical extraction names, journal signatures, row identities,
// Datasets and the editable draft remain unchanged.
func ExportItems(items *interfaces.DraftItems, options *ExportOptions) (*interfaces.DraftItems, error) {
	raw, err := json.Marshal(items)
	if err != nil {
		return nil, err
	}
	var projected interfaces.DraftItems
	if err := json.Unmarshal(raw, &projected); err != nil {
		return nil, err
	}
	aliases := map[string]string{}
	for _, object := range projected.ObjectTypes {
		if object == nil || !object.Adopted || object.Document == nil {
			continue
		}
		for _, property := range object.Properties {
			if property == nil || property.Name != "_id" {
				continue
			}
			alias := property.MappedField
			if !property.ReadOnly || alias == "" || (!fieldName.MatchString(alias) || strings.HasSuffix(alias, "_")) {
				return nil, fmt.Errorf("document object %q has no prepared identity mirror", object.ID)
			}
			for _, other := range object.Properties {
				if other != property && other != nil && other.Name == alias {
					return nil, fmt.Errorf("document object %q has a conflicting identity mirror", object.ID)
				}
			}
			aliases[object.ID] = alias
			property.Name = alias
			for i, key := range object.PrimaryKeys {
				if key == "_id" {
					object.PrimaryKeys[i] = alias
				}
			}
			if object.DisplayKey == "_id" {
				object.DisplayKey = alias
			}
		}
	}
	rename := func(object, property string) string {
		if property == "_id" && aliases[object] != "" {
			return aliases[object]
		}
		return property
	}
	for _, relation := range projected.RelationTypes {
		if relation == nil || !relation.Adopted {
			continue
		}
		for i := range relation.Mapping {
			relation.Mapping[i].SourceProperty = rename(relation.Source, relation.Mapping[i].SourceProperty)
			relation.Mapping[i].TargetProperty = rename(relation.Target, relation.Mapping[i].TargetProperty)
		}
		for i := range relation.SourceMapping {
			relation.SourceMapping[i].SourceProperty = rename(relation.Source, relation.SourceMapping[i].SourceProperty)
		}
		for i := range relation.TargetMapping {
			relation.TargetMapping[i].TargetProperty = rename(relation.Target, relation.TargetMapping[i].TargetProperty)
		}
	}
	if options != nil {
		if err := publishEvidence(&projected, aliases, *options); err != nil {
			return nil, err
		}
	}
	return &projected, nil
}

// publishEvidence leaves out of the projection what a published network would hold empty or
// agents cannot use. It only clears Adopted and drops properties of the copy: the compiler leaves
// out a relation whose ends are not adopted.
func publishEvidence(items *interfaces.DraftItems, aliases map[string]string, options ExportOptions) error {
	objects := map[string]*interfaces.DraftObjectType{}
	roles := map[string]string{}
	for _, object := range items.ObjectTypes {
		if object == nil || !object.Adopted || object.Document == nil {
			continue
		}
		objects[object.ID], roles[object.Document.Role] = object, object.ID
		if originalRoles[object.Document.Role] && !options.IncludeOriginal {
			object.Adopted = false
		}
	}
	adopted := func(id string) bool { return objects[id] != nil && objects[id].Adopted }
	sources, err := sourceRecords(items, objects, aliases, roles, adopted)
	if err != nil {
		return err
	}
	for _, relation := range items.RelationTypes {
		if relation == nil || !relation.Adopted || relation.Document == nil || relation.Document.Role != "evidence_reference" {
			continue
		}
		source := objects[relation.Source]
		// Fused objects reach their files through their source records instead. Image
		// references are never written, so they would always be empty.
		if (source != nil && source.Document.Role == "entity") || (objects[relation.Target] != nil && objects[relation.Target].Document.Role == "evidence_image") {
			relation.Adopted = false
		}
	}
	items.ObjectTypes = append(items.ObjectTypes, sources.objects...)
	items.RelationTypes = append(items.RelationTypes, sources.relations...)
	// The compiler leaves out a relation with an end not adopted; so does the projection, so that
	// what reads it, such as the published bindings check, expects only what was compiled.
	compiled := map[string]bool{}
	for _, object := range items.ObjectTypes {
		if object != nil && object.Adopted {
			compiled[object.ID] = true
		}
	}
	for _, relation := range items.RelationTypes {
		if relation != nil && relation.Adopted && (!compiled[relation.Source] || !compiled[relation.Target]) {
			relation.Adopted = false
		}
	}
	// A column a compiled relation maps, a key or the display key stays declared.
	used := map[string]map[string]bool{}
	use := func(object, property string) {
		if used[object] == nil {
			used[object] = map[string]bool{}
		}
		used[object][property] = true
	}
	for _, relation := range items.RelationTypes {
		if relation == nil || !relation.Adopted || !adopted(relation.Source) || !adopted(relation.Target) {
			continue
		}
		for _, m := range relation.Mapping {
			use(relation.Source, m.SourceProperty)
			use(relation.Target, m.TargetProperty)
		}
		for _, m := range relation.SourceMapping {
			use(relation.Source, m.SourceProperty)
		}
		for _, m := range relation.TargetMapping {
			use(relation.Target, m.TargetProperty)
		}
	}
	for _, object := range objects {
		if !object.Adopted {
			continue
		}
		for _, key := range append([]string{object.DisplayKey}, object.PrimaryKeys...) {
			use(object.ID, key)
		}
		kept := object.Properties[:0]
		for _, property := range object.Properties {
			if property != nil && HiddenColumn(object.Document.Role, property.Name) && !used[object.ID][property.Name] {
				continue
			}
			kept = append(kept, property)
		}
		object.Properties = kept
	}
	return nil
}

// EvidenceExport is how confirming a draft against selection publishes its document evidence.
func EvidenceExport(selection *interfaces.KBSelection) *ExportOptions {
	options := &ExportOptions{}
	if selection == nil {
		return options
	}
	for _, source := range selection.Ranges {
		options.IncludeOriginal = options.IncludeOriginal || source.IncludeOriginal
	}
	for _, file := range selection.Files {
		options.IncludeOriginal = options.IncludeOriginal || file.IncludeOriginal
	}
	return options
}
