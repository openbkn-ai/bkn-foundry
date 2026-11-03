// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbdataset

import (
	"reflect"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// VerifyBindings checks the physical resources and fields actually compiled
// from the draft. Matching semantic fingerprints alone do not prove that the
// generation being read or written is the generation a release will bind.
func VerifyBindings(draft *interfaces.Draft, plan *interfaces.DraftDatasetPlan) error {
	bound, err := BindPreview(draft, plan)
	if err != nil {
		return err
	}
	fieldsEqual := func(left, right []*interfaces.DraftProperty) bool {
		if len(left) != len(right) {
			return false
		}
		for i, field := range left {
			if field == nil || right[i] == nil || field.Name != right[i].Name || field.MappedField != right[i].MappedField {
				return false
			}
		}
		return true
	}
	for i, object := range draft.Items.ObjectTypes {
		if object == nil || !object.Adopted || object.Document == nil {
			continue
		}
		expected := bound.Items.ObjectTypes[i]
		if object.Table != expected.Table || !fieldsEqual(object.Properties, expected.Properties) {
			return ErrStalePreparation
		}
	}
	for i, relation := range draft.Items.RelationTypes {
		if !relationStorage(relation) {
			continue
		}
		expected := bound.Items.RelationTypes[i]
		if !reflect.DeepEqual(relation.LinkTable, expected.LinkTable) || !reflect.DeepEqual(relation.SourceMapping, expected.SourceMapping) || !reflect.DeepEqual(relation.TargetMapping, expected.TargetMapping) || !fieldsEqual(relation.Properties, expected.Properties) {
			return ErrStalePreparation
		}
	}
	return nil
}
