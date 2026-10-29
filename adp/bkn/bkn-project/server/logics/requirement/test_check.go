// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"fmt"
	"slices"
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
	checkExcludedField          = "excluded_field"
	checkNoValueInData          = "no_value_in_data"
	checkNoAbsentValue          = "no_absent_value"
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
			// A field the privacy rules excluded is never read, whatever value the slot has.
			for _, e := range draft.Items.Excluded {
				if e.ObjectType == ot.ID && e.Field == p.MappedField {
					return nil, checkExcludedField
				}
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
	if reason := s.fill(ctx, caller, t, fields); reason != "" {
		return notChecked(reason)
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

// orderField orders the rows values are taken from, so the same data gives the same value: the
// object type's identity when it is one field, else the field itself.
func orderField(f *slotField) string {
	ot := f.objectType
	if len(ot.PrimaryKeys) == 1 {
		for _, p := range ot.Properties {
			if p.Name == ot.PrimaryKeys[0] && p.MappedField != "" {
				return p.MappedField
			}
		}
	}
	return f.field
}

func valueText(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		return v, v != ""
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), true
	case int64:
		return strconv.FormatInt(v, 10), true
	case int:
		return strconv.Itoa(v), true
	case bool:
		return strconv.FormatBool(v), true
	}
	return "", false
}

// sampleRow reads the first row's values of the given fields where they are all set, beside the
// conditions, as the caller. A value goes into the test question only; none goes to the Agent.
func (s *requirementService) sampleRow(ctx context.Context, caller interfaces.AccountInfo, table string,
	order string, fields map[int]string, conditions []string) (map[int]string, string) {
	selects, where := []string{}, append([]string{}, conditions...)
	indexes := []int{}
	for i := range fields {
		indexes = append(indexes, i)
	}
	slices.Sort(indexes)
	for _, i := range indexes {
		selects = append(selects, fmt.Sprintf("%s AS v%d", quoteField(fields[i]), i))
		where = append(where, quoteField(fields[i])+" IS NOT NULL")
	}
	rows, err := s.va.Query(ctx, caller, fmt.Sprintf("SELECT %s FROM {{%s}} WHERE %s ORDER BY %s LIMIT 1",
		strings.Join(selects, ", "), table, strings.Join(where, " AND "), quoteField(order)), checkQueryTimeout)
	if err != nil {
		logger.Warnf("Read a value for a test question failed: %v", err)
		return nil, checkQueryFailed
	}
	if len(rows) == 0 {
		return nil, checkNoValueInData
	}
	values := map[int]string{}
	for _, i := range indexes {
		v, ok := valueText(rows[0][fmt.Sprintf("v%d", i)])
		if !ok {
			return nil, checkNoValueInData
		}
		values[i] = v
	}
	return values, ""
}

// absentValues are the forms of a real value tried for a record that should not exist: its last
// run of digits all nines, then all zeros, keeping its format. A value without digits has none.
func absentValues(value string) []string {
	runes := []rune(value)
	end := -1
	for i := len(runes) - 1; i >= 0; i-- {
		if runes[i] >= '0' && runes[i] <= '9' {
			end = i
			break
		}
	}
	if end < 0 {
		return nil
	}
	start := end
	for start > 0 && runes[start-1] >= '0' && runes[start-1] <= '9' {
		start--
	}
	forms := []string{}
	for _, d := range []string{"9", "0"} {
		forms = append(forms, string(runes[:start])+strings.Repeat(d, end-start+1)+string(runes[end+1:]))
	}
	return forms
}

// fill gives the test question's empty slots values from the data, by what it expects: a value of
// the field for a record that exists, a changed real value no row has for one that does not, and
// one row's values for two ways of naming a record.
func (s *requirementService) fill(ctx context.Context, caller interfaces.AccountInfo, t *interfaces.QuestionTest,
	fields []*slotField) string {
	empty := map[int]bool{}
	for i, f := range fields {
		if f.value == "" {
			empty[i] = true
		}
	}
	if len(empty) == 0 {
		return ""
	}
	set := func(i int, value string) {
		fields[i].value, t.Slots[i].Value = value, value
	}
	if t.Expect == interfaces.TEST_EXPECT_SAME_RECORD {
		// Checked later to be one object type; values are taken from one row of its table.
		for _, f := range fields[1:] {
			if f.objectType != fields[0].objectType {
				return ""
			}
		}
		wanted, conditions := map[int]string{}, []string{}
		for i, f := range fields {
			if empty[i] {
				wanted[i] = f.field
			} else {
				conditions = append(conditions, quoteField(f.field)+" = "+quoteValue(f.value))
			}
		}
		values, reason := s.sampleRow(ctx, caller, fields[0].objectType.Table.ResourceID, orderField(fields[0]),
			wanted, conditions)
		if reason != "" {
			return reason
		}
		for i, v := range values {
			set(i, v)
		}
		return ""
	}
	for i, f := range fields {
		if !empty[i] {
			continue
		}
		values, reason := s.sampleRow(ctx, caller, f.objectType.Table.ResourceID, orderField(f),
			map[int]string{i: f.field}, nil)
		if reason != "" {
			return reason
		}
		value := values[i]
		if t.Expect == interfaces.TEST_EXPECT_NOT_EXISTS {
			value = ""
			for _, form := range absentValues(values[i]) {
				n, err := s.count(ctx, caller, fmt.Sprintf("SELECT COUNT(*) AS n FROM {{%s}} WHERE %s = %s",
					f.objectType.Table.ResourceID, quoteField(f.field), quoteValue(form)))
				if err != nil {
					return checkQueryFailed
				}
				if n == 0 {
					value = form
					break
				}
			}
			if value == "" {
				return checkNoAbsentValue
			}
		}
		set(i, value)
	}
	return ""
}
