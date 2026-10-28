// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"fmt"
	"slices"

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// States of a check item.
const (
	checkCounted = "counted"
	// checkNotCounted is an item the check cannot count by design; it is no failure.
	checkNotCounted = "not_counted"
	checkFailed     = "failed"
)

// Why a check item was not counted, stored as codes for the client to word.
const (
	checkReasonCrossCatalog  = "cross_catalog"
	checkReasonSeveralFields = "several_fields"
	checkReasonUnmapped      = "unmapped"
	checkReasonNoKey         = "no_key"
	checkReasonQueryFailed   = "query_failed"
	checkReasonTimeout       = "timeout"
)

// checkInput is what a confirmation wrote, as the check reads it. It is taken when the draft
// is confirmed, since the draft can change after, and a retry checks the same.
type checkInput struct {
	ObjectTypes   []checkObjectType   `json:"object_types"`
	RelationTypes []checkRelationType `json:"relation_types"`
}

type checkObjectType struct {
	ID         string                `json:"id"`
	Table      interfaces.DraftTable `json:"table"`
	KeyFields  []string              `json:"key_fields"`
	Properties []checkProperty       `json:"properties"`
	// Excluded are the fields the privacy rules left out of the object type.
	Excluded []string `json:"excluded"`
}

type checkProperty struct {
	Name  string `json:"name"`
	Field string `json:"field"`
}

type checkRelationType struct {
	ID string `json:"id"`
	// Joins are one for a direct relation, and one to each end for a relation through a link table.
	Joins []checkJoin `json:"joins"`
}

// checkJoin counts how the records of From find their end, the object type of To, by Field.
type checkJoin struct {
	From       interfaces.DraftTable `json:"from"`
	Field      string                `json:"field"`
	To         interfaces.DraftTable `json:"to"`
	Key        string                `json:"key"`
	ObjectType string                `json:"object_type"`
	// Reason is why the join cannot be counted, when it cannot.
	Reason string `json:"reason,omitempty"`
}

type checkResults struct {
	Keys           []checkKeyResult      `json:"keys"`
	Relations      []checkRelationResult `json:"relations"`
	ExcludedMapped []checkExcludedResult `json:"excluded_mapped"`
	Coverage       checkCoverage         `json:"coverage"`
}

// checkCoverage is how many of the items to count — identities and joins — were counted.
type checkCoverage struct {
	Counted int `json:"counted"`
	Total   int `json:"total"`
}

// checkKeyResult is whether an object type's identity is unique and filled.
type checkKeyResult struct {
	ObjectType string   `json:"object_type"`
	Fields     []string `json:"fields"`
	State      string   `json:"state"`
	Reason     string   `json:"reason,omitempty"`
	Rows       int64    `json:"rows"`
	Duplicates int64    `json:"duplicates"`
	Nulls      int64    `json:"nulls"`
}

type checkRelationResult struct {
	RelationType string            `json:"relation_type"`
	Joins        []checkJoinResult `json:"joins"`
}

// checkJoinResult is, of the records of a join, how many refer to nothing (nulls) and how many
// refer to a record their end does not have (orphans).
type checkJoinResult struct {
	ObjectType string  `json:"object_type"`
	State      string  `json:"state"`
	Reason     string  `json:"reason,omitempty"`
	Rows       int64   `json:"rows"`
	Nulls      int64   `json:"nulls"`
	Orphans    int64   `json:"orphans"`
	HitRate    float64 `json:"hit_rate"`
}

// checkExcludedResult is a property that maps a field the privacy rules excluded.
type checkExcludedResult struct {
	ObjectType string `json:"object_type"`
	Property   string `json:"property"`
	Field      string `json:"field"`
}

// checkInputOf takes what confirming the draft writes: the adopted object types, and the adopted
// relations whose ends are adopted.
func checkInputOf(items *interfaces.DraftItems) *checkInput {
	input := &checkInput{ObjectTypes: []checkObjectType{}, RelationTypes: []checkRelationType{}}
	excluded := map[string][]string{}
	for _, e := range items.Excluded {
		excluded[e.ObjectType] = append(excluded[e.ObjectType], e.Field)
	}
	objectTypes := map[string]*interfaces.DraftObjectType{}
	for _, ot := range items.ObjectTypes {
		if !ot.Adopted {
			continue
		}
		objectTypes[ot.ID] = ot
		checked := checkObjectType{
			ID: ot.ID, Table: ot.Table, KeyFields: []string{}, Properties: []checkProperty{},
			Excluded: append([]string{}, excluded[ot.ID]...),
		}
		for _, key := range ot.PrimaryKeys {
			if field := mappedField(ot, key); field != "" {
				checked.KeyFields = append(checked.KeyFields, field)
			}
		}
		for _, p := range ot.Properties {
			checked.Properties = append(checked.Properties, checkProperty{Name: p.Name, Field: p.MappedField})
		}
		input.ObjectTypes = append(input.ObjectTypes, checked)
	}
	for _, rt := range items.RelationTypes {
		source, target := objectTypes[rt.Source], objectTypes[rt.Target]
		if !rt.Adopted || source == nil || target == nil {
			continue
		}
		checked := checkRelationType{ID: rt.ID}
		if rt.LinkTable == nil {
			checked.Joins = []checkJoin{join(source.Table, source, rt.Mapping, true, target)}
		} else {
			// The link table's columns are fields already; each mapping's other side is an end's property.
			checked.Joins = []checkJoin{
				join(*rt.LinkTable, nil, rt.SourceMapping, false, source),
				join(*rt.LinkTable, nil, rt.TargetMapping, true, target),
			}
		}
		input.RelationTypes = append(input.RelationTypes, checked)
	}
	return input
}

// join is a join of from to the end's key by a mapping of one field; fromOwner, when set, names
// the referring side's properties. endIsTarget says which side of the mapping is the end.
func join(from interfaces.DraftTable, fromOwner *interfaces.DraftObjectType, mapping []interfaces.DraftFieldMapping,
	endIsTarget bool, end *interfaces.DraftObjectType) checkJoin {
	j := checkJoin{From: from, To: end.Table, ObjectType: end.ID}
	if len(mapping) != 1 {
		j.Reason = checkReasonSeveralFields
		return j
	}
	referring, key := mapping[0].SourceProperty, mapping[0].TargetProperty
	if !endIsTarget {
		referring, key = key, referring
	}
	j.Field, j.Key = referring, mappedField(end, key)
	if fromOwner != nil {
		j.Field = mappedField(fromOwner, referring)
	}
	if j.Field == "" || j.Key == "" {
		j.Reason = checkReasonUnmapped
	}
	return j
}

func mappedField(ot *interfaces.DraftObjectType, property string) string {
	if p := findProperty(ot, property); p != nil {
		return p.MappedField
	}
	return ""
}

// runCheck counts what input holds. Items previous counted are kept, so a retry counts only
// what failed; out of time, the items left fail as timed out.
func runCheck(ctx context.Context, c counter, input *checkInput, previous *checkResults) *checkResults {
	results := &checkResults{
		Keys: []checkKeyResult{}, Relations: []checkRelationResult{}, ExcludedMapped: []checkExcludedResult{},
	}
	for _, ot := range input.ObjectTypes {
		result := checkKeyResult{ObjectType: ot.ID, Fields: ot.KeyFields}
		if kept := previous.key(ot.ID, ot.KeyFields); kept != nil {
			result = *kept
		} else {
			countKey(ctx, c, ot, &result)
		}
		results.Keys = append(results.Keys, result)

		for _, p := range ot.Properties {
			if slices.Contains(ot.Excluded, p.Field) {
				results.ExcludedMapped = append(results.ExcludedMapped,
					checkExcludedResult{ObjectType: ot.ID, Property: p.Name, Field: p.Field})
			}
		}
	}
	for _, rt := range input.RelationTypes {
		relation := checkRelationResult{RelationType: rt.ID, Joins: []checkJoinResult{}}
		for i, j := range rt.Joins {
			result := checkJoinResult{ObjectType: j.ObjectType}
			if kept := previous.join(rt.ID, i); kept != nil {
				result = *kept
			} else {
				countJoin(ctx, c, rt.ID, j, &result)
			}
			relation.Joins = append(relation.Joins, result)
		}
		results.Relations = append(results.Relations, relation)
	}
	for _, k := range results.Keys {
		results.Coverage.Total++
		if k.State == checkCounted {
			results.Coverage.Counted++
		}
	}
	for _, rel := range results.Relations {
		for _, j := range rel.Joins {
			results.Coverage.Total++
			if j.State == checkCounted {
				results.Coverage.Counted++
			}
		}
	}
	return results
}

func countKey(ctx context.Context, c counter, ot checkObjectType, result *checkKeyResult) {
	if len(ot.KeyFields) == 0 {
		result.State, result.Reason = checkNotCounted, checkReasonNoKey
		return
	}
	if ctx.Err() != nil {
		result.State, result.Reason = checkFailed, checkReasonTimeout
		return
	}
	table := &interfaces.VegaTable{ID: ot.Table.ResourceID}
	var rows, distinct, nulls int64
	var err error
	if len(ot.KeyFields) == 1 {
		rows, distinct, nulls, err = c.keyStats(ctx, table, ot.KeyFields[0])
	} else {
		rows, distinct, nulls, err = c.keyTupleStats(ctx, table, ot.KeyFields)
	}
	if err != nil {
		result.State, result.Reason = checkFailed, failure(ctx, "key of "+ot.ID, err)
		return
	}
	result.State, result.Rows, result.Nulls = checkCounted, rows, nulls
	result.Duplicates = max(rows-nulls-distinct, 0)
}

func countJoin(ctx context.Context, c counter, relationType string, j checkJoin, result *checkJoinResult) {
	switch {
	case j.Reason != "":
		result.State, result.Reason = checkNotCounted, j.Reason
		return
	case j.From.CatalogID != j.To.CatalogID:
		// As drafting: a join across catalogs is not one vega-backend's SQL route counts.
		result.State, result.Reason = checkNotCounted, checkReasonCrossCatalog
		return
	case ctx.Err() != nil:
		result.State, result.Reason = checkFailed, checkReasonTimeout
		return
	}
	rows, nulls, orphans, _, err := c.joinStats(ctx, &interfaces.VegaTable{ID: j.From.ResourceID}, j.Field,
		&interfaces.VegaTable{ID: j.To.ResourceID}, j.Key)
	if err != nil {
		result.State, result.Reason = checkFailed, failure(ctx, "relation "+relationType, err)
		return
	}
	evidence := joinEvidence(rows, nulls, orphans)
	result.State, result.Rows, result.Nulls, result.Orphans, result.HitRate =
		checkCounted, rows, nulls, orphans, evidence.HitRate
}

// failure is the reason code of a failed count; the error itself, which may hold SQL, is logged
// and not shown.
func failure(ctx context.Context, item string, err error) string {
	if ctx.Err() != nil {
		return checkReasonTimeout
	}
	otellog.LogWarn(ctx, fmt.Sprintf("Check %s failed: %v", item, err))
	return checkReasonQueryFailed
}

func (r *checkResults) key(objectType string, fields []string) *checkKeyResult {
	if r == nil {
		return nil
	}
	for i := range r.Keys {
		k := &r.Keys[i]
		if k.ObjectType == objectType && slices.Equal(k.Fields, fields) && k.State == checkCounted {
			return k
		}
	}
	return nil
}

func (r *checkResults) join(relationType string, index int) *checkJoinResult {
	if r == nil {
		return nil
	}
	for i := range r.Relations {
		rel := &r.Relations[i]
		if rel.RelationType == relationType && index < len(rel.Joins) && rel.Joins[index].State == checkCounted {
			return &rel.Joins[index]
		}
	}
	return nil
}

// checkStatus is failed while an item failed; issues when an identity has duplicates or nulls or
// a property maps an excluded field; partial when nothing is wrong but some items were not
// counted, so a pass is never claimed for what was not looked at; passed otherwise. Orphans are
// reported, not judged: a relation's data may be partial by nature.
func checkStatus(results *checkResults) string {
	issues := len(results.ExcludedMapped) > 0
	for _, k := range results.Keys {
		if k.State == checkFailed {
			return interfaces.CHECK_STATUS_FAILED
		}
		issues = issues || k.Duplicates > 0 || k.Nulls > 0
	}
	for _, rel := range results.Relations {
		for _, j := range rel.Joins {
			if j.State == checkFailed {
				return interfaces.CHECK_STATUS_FAILED
			}
		}
	}
	if issues {
		return interfaces.CHECK_STATUS_ISSUES
	}
	if results.Coverage.Counted < results.Coverage.Total {
		return interfaces.CHECK_STATUS_PARTIAL
	}
	return interfaces.CHECK_STATUS_PASSED
}
