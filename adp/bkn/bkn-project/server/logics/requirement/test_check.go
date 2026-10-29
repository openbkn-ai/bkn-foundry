// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// A test question is asked after a release only if the data bears it out: the record it names
// exists, a record expected not to exist does not, two ways of naming a record find one record.
// Checks count rows through vega-backend as the caller; only counts come back, and no value goes
// to the Agent. What cannot be checked is not passed.

// checkQueryTimeout bounds each count, in seconds.
const checkQueryTimeout = 60

// Why a test question failed or was not checked, stored as codes for the client to word.
const (
	checkRecordNotFound         = "record_not_found"
	checkRecordExists           = "record_exists"
	checkDifferentRecords       = "different_records"
	checkNoValues               = "no_values"
	checkUnknownConcept         = "unknown_concept"
	checkConceptNotMapped       = "concept_not_mapped"
	checkItemNotDrafted         = "item_not_drafted"
	checkUnknownProperty        = "unknown_property"
	checkPropertyNotMapped      = "property_not_mapped"
	checkSameRecordNeedsTwo     = "same_record_needs_two_values"
	checkSameRecordNeedsOneItem = "same_record_needs_one_object_type"
	checkCompositeIdentity      = "composite_identity"
	checkQueryFailed            = "query_failed"
)

// slotField is where a slot's value is looked for: the table of the drafted object type its
// concept maps to, and the field its property is mapped from.
type slotField struct {
	objectType *interfaces.DraftObjectType
	field      string
	value      string
}

func notChecked(reason string) interfaces.QuestionTestCheck {
	return interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_NOT_CHECKED, Reason: reason}
}

// resolveSlot finds where a slot's value is stored, or why it cannot be found.
func resolveSlot(spec *interfaces.RequirementSpec, draft *interfaces.Draft,
	slot interfaces.QuestionTestSlot) (*slotField, string) {
	var concept *interfaces.RequirementConcept
	for _, c := range spec.Concepts {
		if c.ID == slot.ConceptID {
			concept = c
		}
	}
	if concept == nil {
		return nil, checkUnknownConcept
	}
	if concept.MapsTo == nil || concept.MapsTo.Kind != interfaces.REQUIREMENT_ITEM_OBJECT_TYPE {
		return nil, checkConceptNotMapped
	}
	if draft == nil {
		return nil, checkItemNotDrafted
	}
	for _, ot := range draft.Items.ObjectTypes {
		if ot.ID != concept.MapsTo.ID {
			continue
		}
		for _, p := range ot.Properties {
			if p.Name != slot.Property {
				continue
			}
			if p.MappedField == "" {
				return nil, checkPropertyNotMapped
			}
			return &slotField{objectType: ot, field: p.MappedField, value: slot.Value}, ""
		}
		return nil, checkUnknownProperty
	}
	return nil, checkItemNotDrafted
}

// quoteField writes a field name as a MySQL identifier; vega-backend transpiles to the catalog's
// dialect.
func quoteField(field string) string {
	return "`" + strings.ReplaceAll(field, "`", "``") + "`"
}

// quoteValue writes a value as a MySQL string literal.
func quoteValue(value string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), "'", "''") + "'"
}

func (s *requirementService) count(ctx context.Context, caller interfaces.AccountInfo, sql string) (int64, error) {
	rows, err := s.va.Query(ctx, caller, sql, checkQueryTimeout)
	if err != nil {
		return 0, err
	}
	if len(rows) != 1 {
		return 0, fmt.Errorf("expected one row, got %d", len(rows))
	}
	switch v := rows[0]["n"].(type) {
	case float64:
		return int64(v), nil
	case int64:
		return v, nil
	case int:
		return int64(v), nil
	case string:
		return strconv.ParseInt(v, 10, 64)
	}
	return 0, fmt.Errorf("count is %T", rows[0]["n"])
}

// checkTest checks one test question against the data, as the caller.
func (s *requirementService) checkTest(ctx context.Context, caller interfaces.AccountInfo,
	spec *interfaces.RequirementSpec, draft *interfaces.Draft, t *interfaces.QuestionTest) interfaces.QuestionTestCheck {
	if len(t.Slots) == 0 {
		return notChecked(checkNoValues)
	}
	fields := make([]*slotField, 0, len(t.Slots))
	for _, slot := range t.Slots {
		f, reason := resolveSlot(spec, draft, slot)
		if reason != "" {
			return notChecked(reason)
		}
		fields = append(fields, f)
	}
	var key string
	if t.Expect == interfaces.TEST_EXPECT_SAME_RECORD {
		if len(fields) < 2 {
			return notChecked(checkSameRecordNeedsTwo)
		}
		for _, f := range fields[1:] {
			if f.objectType != fields[0].objectType {
				return notChecked(checkSameRecordNeedsOneItem)
			}
		}
		ot := fields[0].objectType
		if len(ot.PrimaryKeys) != 1 {
			return notChecked(checkCompositeIdentity)
		}
		for _, p := range ot.Properties {
			if p.Name == ot.PrimaryKeys[0] {
				key = p.MappedField
			}
		}
		if key == "" {
			return notChecked(checkPropertyNotMapped)
		}
	}

	conditions := make([]string, 0, len(fields))
	for _, f := range fields {
		condition := quoteField(f.field) + " = " + quoteValue(f.value)
		conditions = append(conditions, condition)
		n, err := s.count(ctx, caller, fmt.Sprintf("SELECT COUNT(*) AS n FROM {{%s}} WHERE %s",
			f.objectType.Table.ResourceID, condition))
		if err != nil {
			logger.Warnf("Count for test question %s failed: %v", t.ID, err)
			return notChecked(checkQueryFailed)
		}
		switch {
		case t.Expect == interfaces.TEST_EXPECT_NOT_EXISTS && n > 0:
			return interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_FAILED, Reason: checkRecordExists}
		case t.Expect != interfaces.TEST_EXPECT_NOT_EXISTS && n == 0:
			return interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_FAILED, Reason: checkRecordNotFound}
		}
	}
	if key != "" {
		n, err := s.count(ctx, caller, fmt.Sprintf("SELECT COUNT(DISTINCT %s) AS n FROM {{%s}} WHERE %s",
			quoteField(key), fields[0].objectType.Table.ResourceID, strings.Join(conditions, " OR ")))
		if err != nil {
			logger.Warnf("Count for test question %s failed: %v", t.ID, err)
			return notChecked(checkQueryFailed)
		}
		if n != 1 {
			return interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_FAILED, Reason: checkDifferentRecords}
		}
	}
	return interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_PASSED}
}

// checkTests checks every test question, one after another.
func (s *requirementService) checkTests(ctx context.Context, caller interfaces.AccountInfo,
	spec *interfaces.RequirementSpec, draft *interfaces.Draft, tests []*interfaces.QuestionTest) {
	for _, t := range tests {
		t.Check = s.checkTest(ctx, caller, spec, draft, t)
	}
}
