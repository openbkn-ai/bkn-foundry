// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

var errGraphBudget = errors.New("graph result budget exceeded")

// assembleGraph consumes one bounded observation. It never re-fetches objects
// and never treats a truncated page as a complete evidence collection.
func assembleGraph(ctx context.Context, plan *Plan, rows []map[string]any) (*interfaces.CypherQueryResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(rows) > graphMatchBudget {
		return nil, errGraphBudget
	}
	state := requestEvaluationState(ctx)
	if len(rows) > 0 {
		if shared, ok := rows[0][evaluationStateKey].(*expressionEvaluationState); ok {
			state = shared
		}
	}
	withState := make([]map[string]any, len(rows))
	for i, row := range rows {
		withState[i] = evaluationRow(row, state)
	}
	rows = withState
	logical := plan.Result
	if len(logical.Unwinds) > 0 {
		return assembleUnwinds(ctx, plan, rows)
	}
	if logical.Aggregation != nil {
		return assembleAggregateExpressions(ctx, plan, rows)
	}
	result := &interfaces.CypherQueryResult{Columns: make([]interfaces.RawQueryColumn, 0, len(logical.Projections)), Entries: []map[string]any{}, ResultFormat: interfaces.CYPHER_GRAPH_V1, ValueTypes: map[string]*interfaces.CypherValueType{}, Execution: &interfaces.CypherExecution{MatchRows: len(rows), MatchComplete: true, EvidenceComplete: true}}
	for _, projection := range logical.Projections {
		result.Columns = append(result.Columns, interfaces.RawQueryColumn{Name: projection.Alias, Type: projection.Value.Type.Kind})
		result.ValueTypes[projection.Alias] = projection.Value.Type
	}

	type groupState struct {
		Source     map[string]any
		Entry      map[string]any
		Aggregates map[string][]any
		Seen       map[string]map[string]bool
		SortKeys   []any
	}
	groups := []*groupState{}
	groupOf := map[string]int{}
	objects := map[string]string{}
	observedBytes := 0
	newGroup := func(values map[string]any, sortKeys []any) *groupState {
		group := &groupState{Entry: map[string]any{}, Aggregates: map[string][]any{}, Seen: map[string]map[string]bool{}, SortKeys: sortKeys}
		for _, projection := range logical.Projections {
			if projection.Value.Element != nil {
				group.Aggregates[projection.Alias] = []any{}
				group.Seen[projection.Alias] = map[string]bool{}
			} else {
				group.Entry[projection.Alias] = values[projection.Alias]
			}
		}
		return group
	}
	accepted := 0
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if logical.Where != nil {
			condition, err := evaluateGraph(plan, logical.Where, row)
			if err != nil {
				return nil, err
			}
			if condition == nil {
				continue
			}
			boolean, ok := condition.(bool)
			if !ok {
				return nil, expressionTypeError()
			}
			if !boolean {
				continue
			}
		}
		accepted++
		values := map[string]any{}
		identity := []any{}
		for _, projection := range logical.Projections {
			expr := projection.Value
			if expr.Element != nil {
				expr = expr.Element
			}
			value, err := evaluateGraph(plan, expr, row)
			if err != nil {
				return nil, err
			}
			if projection.Value.Aggregate == "percentilecont" || projection.Value.Aggregate == "percentiledisc" {
				point, err := evaluateGraph(plan, projection.Value.Arguments[0], row)
				if err != nil {
					return nil, err
				}
				value = []any{value, point}
			}
			if err := checkGraphObservation(value, objects); err != nil {
				return nil, err
			}
			values[projection.Alias] = value
			if projection.Value.Element == nil {
				identity = append(identity, value)
			}
		}
		encoded, err := marshalExpressionValue(values)
		if err != nil {
			return nil, err
		}
		observedBytes += len(encoded) + 64
		if observedBytes > graphPayloadBudget {
			return nil, errGraphBudget
		}
		sortKeys := make([]any, len(logical.OrderBy))
		for i, order := range logical.OrderBy {
			if order.Alias == "" && !order.Post {
				value, err := evaluateGraph(plan, order.Value, row)
				if err != nil {
					return nil, err
				}
				sortKeys[i] = value
			}
		}
		index := len(groups)
		if logical.HasAggregates {
			key, err := expressionKey(identity)
			if err != nil {
				return nil, err
			}
			if existing, ok := groupOf[key]; ok {
				index = existing
			} else {
				groupOf[key] = index
			}
		}
		if index == len(groups) {
			groups = append(groups, newGroup(values, sortKeys))
			groups[len(groups)-1].Source = row
		}
		group := groups[index]
		for _, projection := range logical.Projections {
			if projection.Value.Element == nil {
				continue
			}
			value := values[projection.Alias]
			if value == nil {
				continue
			}
			if projection.Value.Distinct {
				key, err := expressionKey(value)
				if err != nil {
					return nil, err
				}
				if group.Seen[projection.Alias][key] {
					continue
				}
				group.Seen[projection.Alias][key] = true
			}
			group.Aggregates[projection.Alias] = append(group.Aggregates[projection.Alias], value)
		}
	}
	if logical.Where != nil {
		result.Execution.CandidateRows = len(rows)
		result.Execution.MatchRows = accepted
	}
	if logical.HasAggregates && accepted == 0 {
		onlyAggregates := true
		for _, projection := range logical.Projections {
			if projection.Value.Element == nil {
				onlyAggregates = false
				break
			}
		}
		if onlyAggregates {
			groups = append(groups, newGroup(nil, nil))
		}
	}
	sortKeys := [][]any{}
	sources := []map[string]any{}
	distinct := map[string]bool{}
	for _, group := range groups {
		for _, projection := range logical.Projections {
			if projection.Value.Element == nil {
				continue
			}
			function := projection.Value.Aggregate
			if function == "" {
				function = "collect"
			}
			value, err := aggregateExpressionValues(function, group.Aggregates[projection.Alias])
			if err != nil {
				return nil, err
			}
			group.Entry[projection.Alias] = value
		}
		if logical.Distinct {
			key, err := expressionKey(group.Entry)
			if err != nil {
				return nil, err
			}
			if distinct[key] {
				continue
			}
			distinct[key] = true
		}
		keys := make([]any, len(logical.OrderBy))
		copy(keys, group.SortKeys)
		for i, order := range logical.OrderBy {
			if order.Post {
				row := make(map[string]any, len(group.Source)+len(group.Entry))
				for name, value := range group.Source {
					row[name] = value
				}
				for name, value := range group.Entry {
					row["unwind:"+name] = value
				}
				value, err := evaluateGraph(plan, order.Value, row)
				if err != nil {
					return nil, err
				}
				keys[i] = value
			}
			if order.Alias != "" {
				keys[i] = group.Entry[order.Alias]
			}
		}
		result.Entries = append(result.Entries, group.Entry)
		sources = append(sources, group.Source)
		sortKeys = append(sortKeys, keys)
	}
	if err := sortExpressionRows(result.Entries, sources, sortKeys, logical.OrderBy); err != nil {
		return nil, err
	}
	if logical.Skip > 0 {
		offset := min(logical.Skip, int64(len(result.Entries)))
		if offset > 0 {
			result.Execution.ConclusionsLimited = true
		}
		result.Entries = result.Entries[offset:]
		sources = sources[offset:]
	}
	if int64(len(result.Entries)) > logical.Limit {
		result.Execution.ConclusionsLimited = true
		result.Entries = result.Entries[:logical.Limit]
		sources = sources[:logical.Limit]
	}
	if logical.PostWhere != nil {
		filtered := []map[string]any{}
		for i, entry := range result.Entries {
			row := make(map[string]any, len(sources[i])+len(entry))
			for name, value := range sources[i] {
				row[name] = value
			}
			for name, value := range scopeRow(entry) {
				row[name] = value
			}
			ok, err := evaluateCondition(plan, logical.PostWhere, row)
			if err != nil {
				return nil, err
			}
			if ok {
				filtered = append(filtered, entry)
			}
		}
		result.Entries = filtered
	}

	raw, err := marshalExpressionValue(result)
	if err != nil {
		return nil, err
	}
	if len(raw) > graphPayloadBudget {
		return nil, errGraphBudget
	}
	return result, nil
}

func evaluateGraph(plan *Plan, expr *GraphExpression, row map[string]any) (any, error) {
	switch {
	case expr.Subquery != nil:
		return evaluateSubquery(plan, expr.Subquery, row)
	case expr.Collection != nil:
		return evaluateCollection(plan, expr.Collection, row)
	case expr.Path != nil:
		return materializeGraphPath(plan, expr.Path, row)
	case expr.Binding != "":
		value, ok := row["unwind:"+expr.Binding]
		if !ok {
			return nil, expressionTypeError()
		}
		return value, nil
	case expr.Operation != "":
		return evaluateOperation(plan, expr, row)
	case expr.Column != "":
		value, ok := row[expr.Column]
		if !ok {
			return nil, fmt.Errorf("graph observation is missing a required column")
		}
		return value, nil
	case expr.Object != nil:
		return materializeGraphObject(plan, expr.Object, row)
	case expr.Relation != nil:
		relation := expr.Relation
		source, err := materializeGraphObject(plan, relation.Source, row)
		if err != nil {
			return nil, err
		}
		target, err := materializeGraphObject(plan, relation.Target, row)
		if err != nil {
			return nil, err
		}
		basis := make([]interfaces.CypherRelationBasis, 0, len(relation.Basis))
		for _, key := range relation.Basis {
			sourceValue, sourceOK := row[key.SourceColumn]
			targetValue, targetOK := row[key.TargetColumn]
			if !sourceOK || !targetOK || sourceValue == nil || targetValue == nil {
				return nil, errors.New("graph relation observation is missing its join basis")
			}
			basis = append(basis, interfaces.CypherRelationBasis{SourceProperty: key.SourceProperty, TargetProperty: key.TargetProperty, SourceValue: sourceValue, TargetValue: targetValue})
		}
		return interfaces.CypherRelationValue{Kind: "relation", RelationTypeID: relation.TypeID, Source: source, Target: target, BindingDigest: relation.BindingDigest, Basis: basis}, nil
	case expr.Fields != nil:
		value := map[string]any{}
		for _, name := range sortedGraphKeys(expr.Fields) {
			field, err := evaluateGraph(plan, expr.Fields[name], row)
			if err != nil {
				return nil, err
			}
			value[name] = field
		}
		return value, nil
	default:
		return expr.Literal, nil
	}
}

func materializeGraphObject(plan *Plan, object *GraphObjectPlan, row map[string]any) (interfaces.CypherObjectValue, error) {
	value := interfaces.CypherObjectValue{Kind: "object", NetworkID: plan.NetworkID, Branch: plan.Branch, ObjectTypeID: object.TypeID, Key: map[string]interfaces.CypherKeyValue{}}
	for _, name := range sortedGraphKeys(object.Keys) {
		field := object.Keys[name]
		raw, ok := row[field.Column]
		if !ok || raw == nil {
			return value, errors.New("graph observation requires non-null complete primary keys")
		}
		normalized, err := graphKeyValue(field.Type, raw)
		if err != nil {
			return value, err
		}
		value.Key[name] = normalized
	}
	if len(object.Properties) > 0 {
		value.Properties = map[string]any{}
	}
	for name, field := range object.Properties {
		raw, ok := row[field.Column]
		if !ok {
			return value, errors.New("graph observation is missing a snapshot property")
		}
		value.Properties[name] = raw
	}
	if len(object.InternalProperties) > 0 {
		value.EvaluationProperties = map[string]any{}
	}
	for name, field := range object.InternalProperties {
		raw, ok := row[field.Column]
		if !ok {
			return value, errors.New("graph observation is missing a query property")
		}
		value.EvaluationProperties[name] = raw
	}
	return value, nil
}

func graphKeyValue(typ string, raw any) (interfaces.CypherKeyValue, error) {
	value := interfaces.CypherKeyValue{Type: typ}
	if typ == "string" || typ == "text" {
		text, ok := raw.(string)
		if !ok {
			return value, errors.New("graph string primary key has an incompatible value")
		}
		value.Value = text
		return value, nil
	}
	// Primary-key integer types vary in width. Normalize their exact decimal
	// representation and reject floating point values outside the safe range.
	unsigned := strings.HasPrefix(typ, "uint") || strings.HasPrefix(typ, "unsigned")
	if !unsigned && !strings.HasPrefix(typ, "int") {
		return value, errors.New("graph primary key type is not supported")
	}
	var encoded string
	switch number := raw.(type) {
	case json.Number:
		encoded = number.String()
	case int:
		encoded = strconv.Itoa(number)
	case int8:
		encoded = strconv.FormatInt(int64(number), 10)
	case int16:
		encoded = strconv.FormatInt(int64(number), 10)
	case int32:
		encoded = strconv.FormatInt(int64(number), 10)
	case int64:
		encoded = strconv.FormatInt(number, 10)
	case uint:
		encoded = strconv.FormatUint(uint64(number), 10)
	case uint8:
		encoded = strconv.FormatUint(uint64(number), 10)
	case uint16:
		encoded = strconv.FormatUint(uint64(number), 10)
	case uint32:
		encoded = strconv.FormatUint(uint64(number), 10)
	case uint64:
		encoded = strconv.FormatUint(number, 10)
	case float64:
		if math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number || math.Abs(number) > 9007199254740991 {
			return value, errors.New("graph primary key requires a lossless integer")
		}
		encoded = strconv.FormatFloat(number, 'f', 0, 64)
	default:
		return value, errors.New("graph integer primary key has an incompatible value")
	}
	integer, ok := new(big.Int).SetString(encoded, 10)
	if !ok || (unsigned && integer.Sign() < 0) || (unsigned && !integer.IsUint64()) || (!unsigned && !integer.IsInt64()) {
		return value, errors.New("graph primary key requires a valid integer")
	}
	value.Value = integer.String()
	return value, nil
}

// graphIdentity removes mutable snapshot properties from grouping keys while
// preserving typed keys, relation orientation and map structure.
func graphIdentity(value any) any {
	switch typed := value.(type) {
	case interfaces.CypherPathValue:
		copy := interfaces.CypherPathValue{Kind: typed.Kind, Nodes: make([]interfaces.CypherObjectValue, len(typed.Nodes)), Relationships: make([]interfaces.CypherRelationValue, len(typed.Relationships))}
		for i, node := range typed.Nodes {
			copy.Nodes[i] = graphIdentity(node).(interfaces.CypherObjectValue)
		}
		for i, relation := range typed.Relationships {
			copy.Relationships[i] = graphIdentity(relation).(interfaces.CypherRelationValue)
		}
		return copy
	case []any:
		copy := make([]any, len(typed))
		for i, child := range typed {
			copy[i] = graphIdentity(child)
		}
		return copy
	case interfaces.CypherObjectValue:
		typed.Properties = nil
		typed.EvaluationProperties = nil
		return typed
	case interfaces.CypherRelationValue:
		typed.Basis = nil
		typed.Source.Properties = nil
		typed.Target.Properties = nil
		typed.Source.EvaluationProperties = nil
		typed.Target.EvaluationProperties = nil
		return typed
	case map[string]any:
		mapped := map[string]any{}
		for key, child := range typed {
			mapped[key] = graphIdentity(child)
		}
		return mapped
	default:
		return value
	}
}

// A single identity must not acquire contradictory snapshots in one result.
func checkGraphObservation(value any, seen map[string]string) error {
	switch typed := value.(type) {
	case interfaces.CypherPathValue:
		for _, node := range typed.Nodes {
			if err := checkGraphObservation(node, seen); err != nil {
				return err
			}
		}
		for _, relation := range typed.Relationships {
			if err := checkGraphObservation(relation, seen); err != nil {
				return err
			}
		}
	case interfaces.CypherObjectValue:
		identity, err := json.Marshal(graphIdentity(typed))
		if err != nil {
			return err
		}
		// Different projections may request different property subsets for
		// one identity. Only contradictory overlapping observations conflict.
		for _, properties := range []map[string]any{typed.Properties, typed.EvaluationProperties} {
			for name, value := range properties {
				snapshot, err := expressionKey(value)
				if err != nil {
					return err
				}
				key := string(identity) + "\x00" + name
				if previous, ok := seen[key]; ok && previous != snapshot {
					return errors.New("graph observation contains conflicting object snapshots")
				}
				seen[key] = snapshot
			}
		}
	case interfaces.CypherRelationValue:
		if err := checkGraphObservation(typed.Source, seen); err != nil {
			return err
		}
		return checkGraphObservation(typed.Target, seen)
	case map[string]any:
		for _, child := range typed {
			if err := checkGraphObservation(child, seen); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range typed {
			if err := checkGraphObservation(child, seen); err != nil {
				return err
			}
		}
	}
	return nil
}
