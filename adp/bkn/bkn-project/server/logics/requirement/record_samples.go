// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/projecthooks"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/locale"
)

// documentSamples is how many records of a document object type are read to find one whose
// values a question needs.
const documentSamples = 20

// documentSampler is the edition's reader of the records a knowledge-base build wrote; nil
// without one.
func documentSampler() projecthooks.DocumentRecordSampler {
	hooks := projecthooks.Current().Drafts
	if hooks == nil {
		return nil
	}
	sampler, _ := hooks.(projecthooks.DocumentRecordSampler)
	return sampler
}

// documentTemplate fills a template test whose slots all read one document object type with the
// values of one record its knowledge base holds: the data's SQL cannot read those. It reports
// whether the template is of a document object type at all; filled is nil when no record has
// every value, and err says the records could not be read.
func documentTemplate(ctx context.Context, caller interfaces.AccountInfo, spec *interfaces.RequirementSpec,
	draft *interfaces.Draft, template *interfaces.QuestionTest) (document bool, filled *interfaces.ScenarioTest, err error) {
	var object *interfaces.DraftObjectType
	properties := []string{}
	for _, slot := range template.Slots {
		f, reason := resolveSlot(spec, draft, slot)
		if reason != "" || f.objectType.Document == nil || object != nil && f.objectType != object {
			return false, nil, nil
		}
		object = f.objectType
		properties = append(properties, slot.Property)
	}
	sampler := documentSampler()
	if object == nil || sampler == nil {
		return false, nil, nil
	}
	rows, err := sampler.SampleRecords(ctx, caller, draft, object.ID, documentSamples)
	if err != nil {
		return true, nil, err
	}
	for _, row := range rows {
		text, complete := template.Text, true
		for i, property := range properties {
			value, ok := valueText(row[property])
			if !ok {
				complete = false
				break
			}
			text = strings.ReplaceAll(text, slotPlaceholder(i), value)
		}
		if complete {
			return true, &interfaces.ScenarioTest{ID: recordTestID, Text: text, Expect: interfaces.TEST_EXPECT_EXISTS}, nil
		}
	}
	return true, nil, nil
}

// exampleObject is the object type a question with no template is about: the first adopted one it
// needs, with the property people tell its records by. Nil when it needs none.
func exampleObject(q *interfaces.RequirementQuestion, draft *interfaces.Draft) (*interfaces.DraftObjectType, *interfaces.DraftProperty) {
	if q == nil || draft == nil {
		return nil, nil
	}
	for _, need := range q.Needs {
		if need.Kind != interfaces.REQUIREMENT_ITEM_OBJECT_TYPE {
			continue
		}
		ot := projecthooks.FindObjectType(&draft.Items, need.ID)
		if ot == nil || !ot.Adopted {
			continue
		}
		if p := namingProperty(ot); p != nil {
			return ot, p
		}
	}
	return nil, nil
}

// namingProperty is the property records of an object type are told apart by as people read them:
// its business key when it has one, else the field it is displayed by.
func namingProperty(ot *interfaces.DraftObjectType) *interfaces.DraftProperty {
	names := []string{}
	if len(ot.PrimaryKeys) == 1 && ot.PrimaryKeys[0] != "_id" {
		names = append(names, ot.PrimaryKeys[0])
	}
	names = append(names, ot.DisplayKey)
	for _, name := range names {
		if p := projecthooks.FindProperty(ot, name); p != nil && name != "" && name != "_id" {
			return p
		}
	}
	return nil
}

// exampleTest asks a question whose text names no record of one record the data has, of the first
// object type it needs: the question, then which record. It reports whether the question can be
// asked so; filled is nil when the data has no such record.
func (s *requirementService) exampleTest(ctx context.Context, caller interfaces.AccountInfo, draft *interfaces.Draft,
	q *interfaces.RequirementQuestion) (askable bool, filled *interfaces.ScenarioTest) {
	ot, property := exampleObject(q, draft)
	if ot == nil {
		return false, nil
	}
	value, known := "", false
	if ot.Document != nil {
		sampler := documentSampler()
		if sampler == nil {
			return false, nil
		}
		rows, err := sampler.SampleRecords(ctx, caller, draft, ot.ID, documentSamples)
		if err != nil {
			logger.Warnf("Read a record of %s for question %s failed: %v", ot.ID, q.ID, err)
			return false, nil
		}
		for _, row := range rows {
			if value, known = valueText(row[property.Name]); known {
				break
			}
		}
	} else {
		if property.MappedField == "" || ot.Table.ResourceID == "" || slices.ContainsFunc(draft.Items.Excluded, func(e *interfaces.DraftExcludedField) bool {
			return e.ObjectType == ot.ID && e.Field == property.MappedField
		}) {
			return false, nil
		}
		values, reason := s.sampleRow(ctx, caller, ot.Table.ResourceID, property.MappedField, map[int]string{0: property.MappedField}, nil)
		if reason != "" && reason != checkNoValueInData {
			return false, nil
		}
		value, known = values[0], reason == ""
	}
	if !known {
		return true, nil
	}
	format, ok := locale.RecordExampleTexts[projecthooks.DraftLanguage(draft)]
	if !ok {
		format = locale.RecordExampleTexts[interfaces.NAMING_LANGUAGE_EN_US]
	}
	name := property.DisplayName
	if name == "" {
		name = property.Name
	}
	return true, &interfaces.ScenarioTest{ID: recordTestID, Text: fmt.Sprintf(format, strings.TrimSpace(q.Text), name, value), Expect: interfaces.TEST_EXPECT_EXISTS}
}
