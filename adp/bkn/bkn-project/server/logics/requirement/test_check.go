// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

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
	checkQueryForbidden         = "query_forbidden"
	checkExcludedField          = "excluded_field"
	checkNoValueInData          = "no_value_in_data"
	checkNoAbsentValue          = "no_absent_value"
	checkValueNotInText         = "value_not_in_text"
	checkValueOutsideSlot       = "value_outside_slot"
	checkTimeout                = "check_timeout"
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

// queryFailure is why a read of the data failed: the caller may not read it, or it failed otherwise.
func queryFailure(err error) string {
	var vegaErr *interfaces.VegaError
	if errors.As(err, &vegaErr) && vegaErr.HTTPCode == http.StatusForbidden {
		return checkQueryForbidden
	}
	return checkQueryFailed
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
	case json.Number:
		return v.Int64()
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

// checkTest checks one test question of the scenario question asked against the data, as the
// caller. In a test expected not to exist, the first slot names the record that does not; the
// others keep values of records that do.
func (s *requirementService) checkTest(ctx context.Context, caller interfaces.AccountInfo,
	spec *interfaces.RequirementSpec, draft *interfaces.Draft, q *interfaces.RequirementQuestion, t *interfaces.QuestionTest) interfaces.QuestionTestCheck {
	return s.checkAnchored(ctx, caller, spec, draft, q, t, nil)
}

// anchorOf is the test whose record a same_record test must name: the server's template test,
// once the data bore it out.
func anchorOf(tests []*interfaces.QuestionTest) *interfaces.QuestionTest {
	for _, t := range tests {
		if t.ByServer && t.Kind == interfaces.TEST_KIND_TEMPLATE && t.Check.Outcome == interfaces.TEST_CHECK_PASSED {
			return t
		}
	}
	return nil
}

// anchorConditions select the anchor's record among the rows of an object type: its slots of
// that object type, as values. None without an anchor, or with no slot of it.
func anchorConditions(spec *interfaces.RequirementSpec, draft *interfaces.Draft, anchor *interfaces.QuestionTest,
	ot *interfaces.DraftObjectType) []string {
	if anchor == nil {
		return nil
	}
	conditions := []string{}
	for _, slot := range anchor.Slots {
		if f, reason := resolveSlot(spec, draft, slot); reason == "" && f.objectType == ot && f.value != "" {
			conditions = append(conditions, quoteField(f.field)+" = "+quoteValue(f.value))
		}
	}
	return conditions
}

// checkAnchored checks a test question; a same_record one with a slot to fill names the record of
// anchor when it has one of the same object type: then its slots together must name that one
// record, so a single other way to name it, or several fields that name it together, are checked.
func (s *requirementService) checkAnchored(ctx context.Context, caller interfaces.AccountInfo,
	spec *interfaces.RequirementSpec, draft *interfaces.Draft, q *interfaces.RequirementQuestion, t *interfaces.QuestionTest,
	anchor *interfaces.QuestionTest) interfaces.QuestionTestCheck {
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
	// What is checked must be what the question asks about: a value the documents gave is said in
	// the text, and one the server fills stands in it as its slot.
	text := normalize([]rune(t.Text)).text
	for i, f := range fields {
		if f.value == "" && !strings.Contains(t.Text, slotPlaceholder(i)) ||
			f.value != "" && !strings.Contains(text, normalize([]rune(f.value)).text) {
			return notChecked(checkValueNotInText)
		}
	}
	if valueOutsideSlots(spec, q.Text, t, fields) {
		return notChecked(checkValueOutsideSlot)
	}
	var anchored []string
	if t.Expect == interfaces.TEST_EXPECT_SAME_RECORD {
		for _, f := range fields[1:] {
			if f.objectType != fields[0].objectType {
				return notChecked(checkSameRecordNeedsOneItem)
			}
		}
		// Only a test with a value to fill names the question's record; one whose values all come
		// from the documents names the record they say, which the sampled anchor need not be.
		if slices.ContainsFunc(fields, func(f *slotField) bool { return f.value == "" }) {
			anchored = anchorConditions(spec, draft, anchor, fields[0].objectType)
		}
	}
	if reason := s.fill(ctx, caller, t, fields, func(f *slotField) *relationPath { return pathOf(draft, q, f) },
		anchored); reason != "" {
		return notChecked(reason)
	}
	var key string
	if t.Expect == interfaces.TEST_EXPECT_SAME_RECORD {
		if len(fields) < 2 && len(anchored) == 0 {
			return notChecked(checkSameRecordNeedsTwo)
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

	if len(anchored) > 0 {
		return s.checkSameAsAnchor(ctx, caller, t, fields, key, anchored)
	}
	conditions := make([]string, 0, len(fields))
	for i, f := range fields {
		condition := quoteField(f.field) + " = " + quoteValue(f.value)
		conditions = append(conditions, condition)
		if t.Expect == interfaces.TEST_EXPECT_NOT_EXISTS && i > 0 {
			break
		}
		n, err := s.count(ctx, caller, fmt.Sprintf("SELECT COUNT(*) AS n FROM {{%s}} WHERE %s",
			f.objectType.Table.ResourceID, condition))
		if err != nil {
			logger.Warnf("Count for test question %s failed: %v", t.ID, err)
			return notChecked(queryFailure(err))
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
			return notChecked(queryFailure(err))
		}
		if n != 1 {
			return interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_FAILED, Reason: checkDifferentRecords}
		}
	}
	return interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_PASSED}
}

// checkSameAsAnchor checks that a same_record test's slots together name one record, by its
// identity field key, and that it is the anchor's.
func (s *requirementService) checkSameAsAnchor(ctx context.Context, caller interfaces.AccountInfo,
	t *interfaces.QuestionTest, fields []*slotField, key string, anchored []string) interfaces.QuestionTestCheck {
	named := make([]string, 0, len(fields))
	for _, f := range fields {
		named = append(named, quoteField(f.field)+" = "+quoteValue(f.value))
	}
	table := fields[0].objectType.Table.ResourceID
	records, err := s.count(ctx, caller, fmt.Sprintf("SELECT COUNT(DISTINCT %s) AS n FROM {{%s}} WHERE %s",
		quoteField(key), table, strings.Join(named, " AND ")))
	if err != nil {
		logger.Warnf("Count for test question %s failed: %v", t.ID, err)
		return notChecked(queryFailure(err))
	}
	switch {
	case records == 0:
		return interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_FAILED, Reason: checkRecordNotFound}
	case records > 1:
		return interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_FAILED, Reason: checkDifferentRecords}
	}
	same, err := s.count(ctx, caller, fmt.Sprintf("SELECT COUNT(*) AS n FROM {{%s}} WHERE %s",
		table, strings.Join(append(named, anchored...), " AND ")))
	if err != nil {
		logger.Warnf("Count for test question %s failed: %v", t.ID, err)
		return notChecked(queryFailure(err))
	}
	if same == 0 {
		return interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_FAILED, Reason: checkDifferentRecords}
	}
	return interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_PASSED}
}

// valueOutsideSlots tells whether the test's text says a number, date, time or code that none of
// its slots holds, the question asked does not say and no rule states: no one checks such a value.
func valueOutsideSlots(spec *interfaces.RequirementSpec, asked string, t *interfaces.QuestionTest, fields []*slotField) bool {
	text := t.Text
	for i := range fields {
		text = strings.ReplaceAll(text, slotPlaceholder(i), " ")
	}
	text = normalize([]rune(text)).text
	for _, f := range fields {
		if f.value != "" {
			text = strings.ReplaceAll(text, normalize([]rune(f.value)).text, " ")
		}
	}
	// Compared as whole tokens: 3 is not stated by a question that says 30. A number with a unit
	// joined to it is its number: a 14-day window is stated by the next 14 days.
	stated := []string{}
	for _, s := range append([]string{asked}, rulesText(spec)...) {
		for _, token := range valueTokens(normalize([]rune(s)).text) {
			stated = append(stated, numberOf(token))
		}
	}
	for _, token := range valueTokens(text) {
		if !slices.Contains(stated, numberOf(token)) {
			return true
		}
	}
	return false
}

func rulesText(spec *interfaces.RequirementSpec) []string {
	texts := []string{}
	for _, r := range spec.Rules {
		texts = append(texts, r.Statement)
	}
	return texts
}

// units are the unit words a number may have joined to it, as in 14-day, 30d or 3.5kg.
var units = map[string]bool{
	"s": true, "sec": true, "min": true, "mins": true, "minute": true, "minutes": true, "h": true, "hr": true,
	"hrs": true, "hour": true, "hours": true, "d": true, "day": true, "days": true, "w": true, "wk": true,
	"week": true, "weeks": true, "mo": true, "month": true, "months": true, "y": true, "yr": true, "year": true,
	"years": true, "mg": true, "g": true, "kg": true, "t": true, "mm": true, "cm": true, "m": true, "km": true,
	"ml": true, "l": true, "pc": true, "pcs": true, "x": true,
}

// unitNumber is a number with a word joined after it.
var unitNumber = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)-?([A-Za-z]+)$`)

// numberOf is a value token's number when it is one with a unit, else the token: a code such as
// A14 or 999-A keeps its letters.
func numberOf(token string) string {
	if m := unitNumber.FindStringSubmatch(token); m != nil && units[strings.ToLower(m[2])] {
		return m[1]
	}
	return token
}

// valueTokens lists the runs of a text that hold a digit, with the ASCII letters and the - / . : _
// marks joined to it, as in 2025-06-30, 10:30, 3.5 or CUST-10001.
func valueTokens(text string) []string {
	tokens := []string{}
	var b strings.Builder
	digit := false
	flush := func() {
		if digit {
			tokens = append(tokens, strings.Trim(b.String(), "-/.:_"))
		}
		b.Reset()
		digit = false
	}
	for _, r := range text {
		switch {
		case r >= '0' && r <= '9':
			digit = true
			b.WriteRune(r)
		case r < utf8.RuneSelf && (unicode.IsLetter(r) || strings.ContainsRune("-/.:_", r)):
			b.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	return tokens
}

// checkTests checks every test question, one after another, until deadline: those left are not
// checked, so the run records its outcome before its lease runs out.
func (s *requirementService) checkTests(ctx context.Context, caller interfaces.AccountInfo,
	spec *interfaces.RequirementSpec, draft *interfaces.Draft, q *interfaces.RequirementQuestion, tests []*interfaces.QuestionTest, deadline int64) {
	ctx, cancel := context.WithDeadline(ctx, time.UnixMilli(deadline))
	defer cancel()
	s.useExamples(ctx, caller, spec, draft, q, tests)
	for _, t := range tests {
		if s.now() >= deadline {
			t.Check = notChecked(checkTimeout)
			continue
		}
		t.Check = s.checkAnchored(ctx, caller, spec, draft, q, t, anchorOf(tests))
	}
}

// useExamples puts the records the documents give as examples into the server's tests, in place of
// values sampled from the data: into a slot of a record that exists when the data has it, and as
// the record that does not exist (the not_exist test's first slot) when it lacks it. A slot with a
// value, or an example the data cannot be asked about, is left as it is.
func (s *requirementService) useExamples(ctx context.Context, caller interfaces.AccountInfo,
	spec *interfaces.RequirementSpec, draft *interfaces.Draft, q *interfaces.RequirementQuestion, tests []*interfaces.QuestionTest) {
	if q == nil || q.Inputs == nil {
		return
	}
	exists := map[string]bool{}
	known := map[string]bool{}
	for _, t := range tests {
		if !t.ByServer {
			continue
		}
		for i, slot := range t.Slots {
			k := slices.IndexFunc(*q.Inputs, func(in interfaces.RequirementInput) bool {
				return in.Example != "" && in.ConceptID == slot.ConceptID && in.Property == slot.Property
			})
			if slot.Value != "" || k < 0 {
				continue
			}
			example := (*q.Inputs)[k].Example
			if _, counted := known[example]; !counted {
				known[example] = false
				if f, reason := resolveSlot(spec, draft, slot); reason == "" {
					n, err := s.count(ctx, caller, fmt.Sprintf("SELECT COUNT(*) AS n FROM {{%s}} WHERE %s = %s",
						f.objectType.Table.ResourceID, quoteField(f.field), quoteValue(example)))
					known[example], exists[example] = err == nil, n > 0
				}
			}
			absent := t.Kind == interfaces.TEST_KIND_NOT_EXIST && i == 0
			if known[example] && exists[example] != absent {
				t.Slots[i].Value = example
				t.Text = strings.ReplaceAll(t.Text, slotPlaceholder(i), example)
			}
		}
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
	case json.Number:
		return v.String(), v != ""
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
		return nil, queryFailure(err)
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

// relationPath is how the rows of a slot's object type are kept to those the question's path
// reaches: the slot's field a relation joins on, and the values it is joined with, as a query of
// the object type at the other end (objectType) and the column it selects.
type relationPath struct {
	// through is set for a path through an object type the question does not need: the caller may
	// not read it, and it may be large, so a failed read falls back to any record.
	through    bool
	objectType string
	own        string
	reached    string
	column     string
}

// sample reads the first value of field among the rows of table the path reaches, ordered by order.
// vega-backend parses no subquery in WHERE, so the rows are joined with the reached values, as
// drafting's join counts are. It holds no value, only names.
func (p *relationPath) sample(table string, field string, order string) string {
	return p.sampleRow(table, []string{field}, []string{"v"}, order)
}

// sampleRow reads the first row's fields, each as its alias, among the rows of table the path
// reaches where they are all set, ordered by order.
func (p *relationPath) sampleRow(table string, fields []string, aliases []string, order string) string {
	selects, set := []string{}, []string{}
	for i, f := range fields {
		selects = append(selects, "a."+quoteField(f)+" AS "+aliases[i])
		set = append(set, "a."+quoteField(f)+" IS NOT NULL")
	}
	return fmt.Sprintf("SELECT %s FROM {{%s}} a JOIN (%s) b ON a.%s = b.%s WHERE %s ORDER BY a.%s LIMIT 1",
		strings.Join(selects, ", "), table, p.reached, quoteField(p.own), quoteField(p.column),
		strings.Join(set, " AND "), quoteField(order))
}

// pathOf finds where a slot's values are taken from, so that the question can follow them: the
// first relation the question names, in its order, that joins the slot's object type to another;
// else the route its path takes (graph.route, as questionPath finds it) to the first other object
// type it needs: a relation between them, or one through an object type between. Only relations
// on one field are followed, not through a link table, nor across catalogs: one query cannot read
// two.
func pathOf(draft *interfaces.Draft, q *interfaces.RequirementQuestion, f *slotField) *relationPath {
	if draft == nil {
		return nil
	}
	field := func(objectType string, property string) (*interfaces.DraftObjectType, string) {
		for _, ot := range draft.Items.ObjectTypes {
			if ot.ID != objectType {
				continue
			}
			for _, p := range ot.Properties {
				if p.Name == property && p.MappedField != "" && ot.Table.ResourceID != "" &&
					ot.Table.CatalogID == f.objectType.Table.CatalogID {
					return ot, p.MappedField
				}
			}
		}
		return nil, ""
	}
	// join reads a relation from one of its ends: that end's field, and the other end with its field.
	join := func(rt *interfaces.DraftRelationType, from string) (string, *interfaces.DraftObjectType, string) {
		if rt.LinkTable != nil || len(rt.Mapping) != 1 || rt.Source == rt.Target {
			return "", nil, ""
		}
		own, other, otherType := rt.Mapping[0].SourceProperty, rt.Mapping[0].TargetProperty, rt.Target
		if rt.Target == from {
			own, other, otherType = other, own, rt.Source
		} else if rt.Source != from {
			return "", nil, ""
		}
		_, ownField := field(from, own)
		ot, otherField := field(otherType, other)
		if ownField == "" || ot == nil {
			return "", nil, ""
		}
		return ownField, ot, otherField
	}
	for _, need := range q.Needs {
		if need.Kind != interfaces.REQUIREMENT_ITEM_RELATION_TYPE {
			continue
		}
		for _, rt := range draft.Items.RelationTypes {
			if rt.ID != need.ID {
				continue
			}
			if own, ot, other := join(rt, f.objectType.ID); ot != nil {
				return &relationPath{objectType: ot.ID, own: own,
					reached: fmt.Sprintf("SELECT DISTINCT %s FROM {{%s}}", quoteField(other), ot.Table.ResourceID), column: other}
			}
		}
	}
	// Else the way to another object type the question needs, as its path is found: a relation
	// between them, or one through an object type between, adopted and that one query can read.
	g := graphOf(draft)
	readable := func(h hop) bool {
		_, ot, _ := join(h.relation, h.from)
		return ot != nil
	}
	for _, need := range q.Needs {
		if need.Kind != interfaces.REQUIREMENT_ITEM_OBJECT_TYPE || need.ID == f.objectType.ID {
			continue
		}
		r := g.route(f.objectType.ID, need.ID, readable)
		switch len(r) {
		case 1:
			own, ot, other := join(r[0].relation, f.objectType.ID)
			return &relationPath{through: true, objectType: ot.ID, own: own,
				reached: fmt.Sprintf("SELECT DISTINCT %s FROM {{%s}}", quoteField(other), ot.Table.ResourceID), column: other}
		case 2:
			own, between, toSlot := join(r[0].relation, f.objectType.ID)
			toNext, ot, other := join(r[1].relation, between.ID)
			// The rows between that refer to a record of the needed object type, and the slot's
			// values they refer to.
			reached := fmt.Sprintf("SELECT DISTINCT i.%s FROM {{%s}} i JOIN (SELECT DISTINCT %s FROM {{%s}}) c ON i.%s = c.%s",
				quoteField(toSlot), between.Table.ResourceID, quoteField(other), ot.Table.ResourceID,
				quoteField(toNext), quoteField(other))
			return &relationPath{through: true, objectType: between.ID, own: own, reached: reached, column: toSlot}
		}
	}
	return nil
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
// the field for a record that exists, of a record the question's path reaches when it has one, a
// changed real value no row has for one that does not, and one row's values for two ways of naming
// a record.
//
// A same_record test anchored on the question's record (anchor: conditions that select it) takes
// its values from that record's row. Otherwise the slots of one object type take theirs from one
// row, so the values of one record's slots belong to that record.
func (s *requirementService) fill(ctx context.Context, caller interfaces.AccountInfo, t *interfaces.QuestionTest,
	fields []*slotField, path func(*slotField) *relationPath, anchor []string) string {
	empty := map[int]bool{}
	for i, f := range fields {
		if f.value == "" {
			empty[i] = true
			t.Slots[i].ValueFrom = ""
		}
	}
	if len(empty) == 0 {
		return ""
	}
	set := func(i int, value string) {
		fields[i].value, t.Slots[i].Value = value, value
		t.Text = strings.ReplaceAll(t.Text, slotPlaceholder(i), value)
	}
	if t.Expect == interfaces.TEST_EXPECT_SAME_RECORD {
		// Checked later to be one object type; values are taken from one row of its table.
		for _, f := range fields[1:] {
			if f.objectType != fields[0].objectType {
				return ""
			}
		}
		wanted, conditions := map[int]string{}, append([]string{}, anchor...)
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
	// The record that does not exist is named by the first slot; the others are of ones that do.
	if t.Expect == interfaces.TEST_EXPECT_NOT_EXISTS && empty[0] {
		f := fields[0]
		values, reason := s.sampleRow(ctx, caller, f.objectType.Table.ResourceID, orderField(f),
			map[int]string{0: f.field}, nil)
		if reason != "" {
			return reason
		}
		value := ""
		for _, form := range absentValues(values[0]) {
			n, err := s.count(ctx, caller, fmt.Sprintf("SELECT COUNT(*) AS n FROM {{%s}} WHERE %s = %s",
				f.objectType.Table.ResourceID, quoteField(f.field), quoteValue(form)))
			if err != nil {
				return queryFailure(err)
			}
			if n == 0 {
				value = form
				break
			}
		}
		if value == "" {
			return checkNoAbsentValue
		}
		set(0, value)
		delete(empty, 0)
	}
	// The values given of a record that exists, by object type: its empty slots are taken from their row.
	given := map[*interfaces.DraftObjectType][]string{}
	for i, f := range fields {
		if !empty[i] && !(t.Expect == interfaces.TEST_EXPECT_NOT_EXISTS && i == 0) {
			given[f.objectType] = append(given[f.objectType], quoteField(f.field)+" = "+quoteValue(f.value))
		}
	}
	var groups [][]int
	for i, f := range fields {
		if !empty[i] {
			continue
		}
		g := slices.IndexFunc(groups, func(g []int) bool { return fields[g[0]].objectType == f.objectType })
		if g < 0 {
			groups = append(groups, []int{i})
		} else {
			groups[g] = append(groups[g], i)
		}
	}
	for _, g := range groups {
		f := fields[g[0]]
		if p := path(f); p != nil && len(given[f.objectType]) == 0 {
			// A record the question's path reaches, so asking about it can show something.
			names, aliases := []string{}, []string{}
			for _, i := range g {
				names, aliases = append(names, fields[i].field), append(aliases, "v")
			}
			if len(g) > 1 {
				for k, i := range g {
					aliases[k] = fmt.Sprintf("v%d", i)
				}
			}
			sql := p.sampleRow(f.objectType.Table.ResourceID, names, aliases, orderField(f))
			rows, err := s.va.Query(ctx, caller, sql, checkQueryTimeout)
			if err != nil {
				logger.Warnf("Read a value along a relation for a test question failed: %v; statement: %s", err, sql)
				if !p.through {
					return queryFailure(err)
				}
			}
			if len(rows) > 0 {
				values := map[int]string{}
				for k, i := range g {
					if v, ok := valueText(rows[0][aliases[k]]); ok {
						values[i] = v
					}
				}
				if len(values) == len(g) {
					for i, v := range values {
						set(i, v)
						t.Slots[i].ValueFrom = p.objectType
					}
					continue
				}
			}
		}
		wanted := map[int]string{}
		for _, i := range g {
			wanted[i] = fields[i].field
		}
		values, reason := s.sampleRow(ctx, caller, f.objectType.Table.ResourceID, orderField(f), wanted, given[f.objectType])
		if reason == checkNoValueInData && len(given[f.objectType]) > 0 {
			// No row has the values given: the check says so, with the others from any row.
			values, reason = s.sampleRow(ctx, caller, f.objectType.Table.ResourceID, orderField(f), wanted, nil)
		}
		if reason != "" {
			return reason
		}
		for i, v := range values {
			set(i, v)
		}
	}
	return ""
}
