// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// signedItem is what a question's test questions and guidance rely on of one drafted item: its
// id, whether it is adopted, its identity, and the properties by their names, types and fields, or
// the relation's ends and the properties it joins. Names people read and descriptions are left out:
// renaming an item changes nothing the tests ask by.
type signedItem struct {
	Kind        string                           `json:"kind"`
	ID          string                           `json:"id"`
	Missing     bool                             `json:"missing,omitempty"`
	Adopted     bool                             `json:"adopted,omitempty"`
	PrimaryKeys []string                         `json:"primary_keys,omitempty"`
	Properties  []signedProperty                 `json:"properties,omitempty"`
	Source      string                           `json:"source,omitempty"`
	Target      string                           `json:"target,omitempty"`
	LinkTable   string                           `json:"link_table,omitempty"`
	Mapping     [][]interfaces.DraftFieldMapping `json:"mapping,omitempty"`
}

type signedProperty struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Field string `json:"field"`
}

// ontologySignature is the SHA-256 of the drafted items the question needs, or of the adopted
// ones when it names none, so that guidance can tell when the ontology it was generated on has
// changed. A need the draft lacks is signed as missing. Empty without a draft.
func ontologySignature(draft *interfaces.Draft, q *interfaces.RequirementQuestion) string {
	if draft == nil {
		return ""
	}
	needed := map[interfaces.RequirementItemRef]bool{}
	for _, need := range q.Needs {
		needed[need] = true
	}
	include := func(kind string, id string, adopted bool) bool {
		if len(q.Needs) == 0 {
			return adopted
		}
		return needed[interfaces.RequirementItemRef{Kind: kind, ID: id}]
	}
	items := []signedItem{}
	found := map[interfaces.RequirementItemRef]bool{}
	for _, ot := range draft.Items.ObjectTypes {
		if !include(interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ot.ID, ot.Adopted) {
			continue
		}
		found[interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: ot.ID}] = true
		item := signedItem{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: ot.ID, Adopted: ot.Adopted,
			PrimaryKeys: ot.PrimaryKeys, Properties: []signedProperty{}}
		for _, p := range ot.Properties {
			item.Properties = append(item.Properties, signedProperty{Name: p.Name, Type: p.Type, Field: p.MappedField})
		}
		slices.SortFunc(item.Properties, func(a, b signedProperty) int { return cmp.Compare(a.Name, b.Name) })
		items = append(items, item)
	}
	for _, rt := range draft.Items.RelationTypes {
		if !include(interfaces.REQUIREMENT_ITEM_RELATION_TYPE, rt.ID, rt.Adopted) {
			continue
		}
		found[interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_RELATION_TYPE, ID: rt.ID}] = true
		item := signedItem{Kind: interfaces.REQUIREMENT_ITEM_RELATION_TYPE, ID: rt.ID, Adopted: rt.Adopted,
			Source: rt.Source, Target: rt.Target,
			Mapping: [][]interfaces.DraftFieldMapping{rt.Mapping, rt.SourceMapping, rt.TargetMapping}}
		if rt.LinkTable != nil {
			item.LinkTable = rt.LinkTable.ResourceID
		}
		items = append(items, item)
	}
	for _, need := range q.Needs {
		if !found[need] {
			found[need] = true
			items = append(items, signedItem{Kind: need.Kind, ID: need.ID, Missing: true})
		}
	}
	slices.SortFunc(items, func(a, b signedItem) int {
		return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.ID, b.ID))
	})
	// Marshalling plain structs and slices cannot fail.
	body, _ := json.Marshal(items)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
