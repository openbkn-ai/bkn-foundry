// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbdataset

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// ExportItems translates the managed write identity to its prepared
// ordinary Dataset field for BKN, whose property names cannot begin with '_'.
// This is a compiler projection: logical extraction names, journal signatures,
// row identities and the editable draft remain unchanged.
func ExportItems(items *interfaces.DraftItems) (*interfaces.DraftItems, error) {
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
	return &projected, nil
}
