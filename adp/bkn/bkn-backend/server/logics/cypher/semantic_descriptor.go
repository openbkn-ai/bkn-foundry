// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package cypher

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

const maxSemanticDescriptorBytes = 64 << 10

// ErrSemanticDescriptorTooLarge reports a query whose descriptor would exceed
// maxSemanticDescriptorBytes. It is the query that is too large, typically an
// IN over many values, so callers report it as a bad request rather than as a
// server failure.
var ErrSemanticDescriptorTooLarge = errors.New("semantic query descriptor too large")

// semanticStringMatchOperators names the string predicates in the descriptor.
var semanticStringMatchOperators = map[StringMatchOperator]string{
	StartsWith: "starts_with", EndsWith: "ends_with", Contains: "contains",
}

var simpleJSONPathField = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// SemanticQueryDescriptor is the bounded ontology plan captured after a
// Cypher query has been parsed, authorized and bound. It contains no physical
// resource or column names.
type SemanticQueryDescriptor struct {
	Version         string                    `json:"version"`
	ProducerProfile string                    `json:"producer_profile"`
	NetworkID       string                    `json:"network_id"`
	Branch          string                    `json:"branch"`
	QueryHash       string                    `json:"query_hash"`
	Objects         []SemanticQueryObject     `json:"objects"`
	Relations       []SemanticQueryRelation   `json:"relations,omitempty"`
	Predicates      []SemanticQueryPredicate  `json:"predicates,omitempty"`
	Projections     []SemanticQueryProjection `json:"projections"`
	Grouping        []string                  `json:"grouping,omitempty"`
	Ordering        []SemanticQueryOrdering   `json:"ordering,omitempty"`
	Skip            *int64                    `json:"skip,omitempty"`
	Limit           *int64                    `json:"limit,omitempty"`
	EffectiveLimit  int64                     `json:"effective_limit"`
	LimitSource     string                    `json:"limit_source"`
	ResultPointer   string                    `json:"result_pointer"`
}

type SemanticQueryObject struct {
	Alias     string `json:"alias"`
	ObjectRef string `json:"object_ref"`
}

type SemanticQueryRelation struct {
	RelationRef string `json:"relation_ref"`
	SourceAlias string `json:"source_alias"`
	TargetAlias string `json:"target_alias"`
	Direction   string `json:"direction"`
	// Optional records that an OPTIONAL MATCH wrote this relationship, so a
	// reader of the descriptor can tell a row that found nothing from one that
	// was never asked for. It is omitted when unset, which keeps the bytes of
	// every descriptor written before outer joins existed unchanged.
	Optional bool `json:"optional,omitempty"`
}

type SemanticQueryPredicate struct {
	PropertyRef string `json:"property_ref"`
	// Expression and PropertyRefs describe a comparison with arithmetic on a
	// side, which reads several properties and so has no one property_ref.
	Expression   string   `json:"expression,omitempty"`
	PropertyRefs []string `json:"property_refs,omitempty"`
	Operator     string   `json:"operator"`
	InputPointer string   `json:"input_pointer"`
	ValueHashes  []string `json:"value_hashes,omitempty"`
	LogicalPath  string   `json:"logical_path,omitempty"`
}

type SemanticQueryProjection struct {
	Alias       string `json:"alias"`
	PropertyRef string `json:"property_ref,omitempty"`
	Aggregate   string `json:"aggregate,omitempty"`
	Distinct    bool   `json:"distinct,omitempty"`
	// Expression is "arithmetic" for a computed column, or for an aggregate
	// over a computed value, and PropertyRefs lists every property it reads.
	// The computation itself is not recorded: it can carry numbers from the
	// query, which the descriptor otherwise only ever holds as hashes. Both
	// are omitted when unset, which keeps the bytes of every descriptor
	// written before arithmetic existed unchanged.
	Expression   string   `json:"expression,omitempty"`
	PropertyRefs []string `json:"property_refs,omitempty"`
	// Value is the kind of a graph value -- node, relation, constant, map or
	// collect -- and RelationRefs the relation types it returns. A constant is
	// recorded by kind only, never by what it says.
	Value         string   `json:"value,omitempty"`
	RelationRefs  []string `json:"relation_refs,omitempty"`
	OutputPointer string   `json:"output_pointer"`
}

type SemanticQueryOrdering struct {
	Alias        string   `json:"alias,omitempty"`
	PropertyRef  string   `json:"property_ref,omitempty"`
	Aggregate    string   `json:"aggregate,omitempty"`
	Expression   string   `json:"expression,omitempty"`
	PropertyRefs []string `json:"property_refs,omitempty"`
	Descending   bool     `json:"descending"`
}

// semanticExpression is what the descriptor records of a computed value, and
// semanticCondition of a condition returned as a value, and semanticComparison
// of one property compared with another.
const (
	semanticExpression = "arithmetic"
	semanticCondition  = "condition"
	semanticComparison = "property_comparison"
	semanticSemiJoin   = "semi_join"
	semanticCase       = "case"
	semanticCoalesce   = "coalesce"
	// semanticCarried marks a value a carried stage named, read after it.
	semanticCarried = "carried"
	// semanticIntersection marks the count of what two collected lists
	// share.
	semanticIntersection = "intersection"
	// semanticGroups marks an aggregate over the groups a WITH made rather
	// than over the matched rows.
	semanticGroups = "groups"
)

// semanticTotals describes what a query returns when it aggregates a WITH's
// groups: the totals, each reading the properties of the group column it
// aggregates. The group columns themselves are not returned.
func semanticTotals(totals []PlanTotal, groups []SemanticQueryProjection) []SemanticQueryProjection {
	projections := make([]SemanticQueryProjection, 0, len(totals))
	for _, total := range totals {
		projection := SemanticQueryProjection{
			Alias: total.Alias, OutputPointer: semanticOutputPointer(total.Alias),
			Aggregate: strings.ToLower(total.Function), Distinct: total.Distinct, Expression: semanticGroups,
		}
		for _, group := range groups {
			if total.Of == "" || group.Alias != total.Of {
				continue
			}
			if group.PropertyRef != "" {
				projection.PropertyRefs = append(projection.PropertyRefs, group.PropertyRef)
			}
			projection.PropertyRefs = append(projection.PropertyRefs, group.PropertyRefs...)
		}
		projections = append(projections, projection)
	}
	return projections
}

// semanticExpressionKind names what a computed column is in the descriptor.
func semanticExpressionKind(value PlanExpression) string {
	switch value.(type) {
	case PlanConditionValue:
		return semanticCondition
	case PlanCase:
		return semanticCase
	case PlanCoalesce:
		return semanticCoalesce
	case PlanCarriedValue:
		return semanticCarried
	}
	return semanticExpression
}

func BuildSemanticQueryDescriptor(plan *Plan, query string) (*SemanticQueryDescriptor, error) {
	if plan != nil && plan.Intersection != nil {
		return semanticIntersectionDescriptor(plan, query)
	}
	if plan != nil && plan.Union != nil {
		return semanticUnionDescriptor(plan, query)
	}
	if plan == nil || strings.TrimSpace(plan.NetworkID) == "" || len(plan.Tables) == 0 || len(plan.Select) == 0 {
		return nil, errors.New("semantic query descriptor requires a bound query plan")
	}
	descriptor := &SemanticQueryDescriptor{
		Version:         "semantic-query-descriptor/v1",
		ProducerProfile: "openbkn.bkn-backend.run_cypher@0.1.5",
		NetworkID:       plan.NetworkID, Branch: plan.Branch,
		QueryHash:   semanticQueryHash(query),
		Objects:     make([]SemanticQueryObject, 0, len(plan.Tables)),
		Relations:   make([]SemanticQueryRelation, 0, len(plan.Joins)),
		Projections: make([]SemanticQueryProjection, 0, len(plan.Select)),
		Skip:        cloneInt64(plan.Skip), Limit: cloneInt64(plan.Limit), ResultPointer: "$.entries",
	}
	if plan.Limit == nil {
		descriptor.EffectiveLimit = int64(interfaces.CYPHER_DEFAULT_LIMIT)
		descriptor.LimitSource = "default"
	} else {
		if *plan.Limit > interfaces.CYPHER_MAX_LIMIT {
			return nil, fmt.Errorf("semantic query descriptor limit exceeds %d", interfaces.CYPHER_MAX_LIMIT)
		}
		descriptor.EffectiveLimit = *plan.Limit
		descriptor.LimitSource = "explicit"
	}
	for index, table := range plan.Tables {
		if table.Backing {
			// Part of the relation that reads it, recorded under Relations.
			continue
		}
		if strings.TrimSpace(table.ObjectTypeID) == "" {
			return nil, fmt.Errorf("semantic query object %d is missing its bound object type", index)
		}
		descriptor.Objects = append(descriptor.Objects, SemanticQueryObject{
			Alias:     semanticAlias(table, index),
			ObjectRef: "object:" + plan.NetworkID + ":" + table.ObjectTypeID,
		})
	}
	for index, relation := range plan.Joins {
		if relation.Left < 0 || relation.Left >= len(plan.Tables) || relation.Right < 0 || relation.Right >= len(plan.Tables) || relation.RelationTypeID == "" {
			return nil, fmt.Errorf("semantic query relation %d is incomplete", index)
		}
		source, target := relation.Left, relation.Right
		direction := "outgoing"
		switch relation.Direction {
		case Incoming:
			source, target, direction = relation.Right, relation.Left, "incoming"
		case Undirected:
			direction = "undirected"
		}
		descriptor.Relations = append(descriptor.Relations, SemanticQueryRelation{
			RelationRef: "relation:" + plan.NetworkID + ":" + relation.RelationTypeID,
			SourceAlias: semanticAlias(plan.Tables[source], source),
			TargetAlias: semanticAlias(plan.Tables[target], target), Direction: direction,
			Optional: relation.Optional,
		})
	}
	if err := appendSemanticPredicates(descriptor, plan, plan.Where, ""); err != nil {
		return nil, err
	}
	// A WITH's condition on aggregates is recorded under its own path, since
	// it filters groups rather than rows.
	if err := appendSemanticPredicates(descriptor, plan, plan.Having, "having"); err != nil {
		return nil, err
	}
	// A condition carried by an outer join decides what that join matched
	// rather than which rows survive. It is recorded under the relationship it
	// qualifies, so the descriptor says which of the two it was.
	for index, relation := range plan.Joins {
		path := semanticPath("", "OPTIONAL", index)
		for position, condition := range relation.Conditions {
			if err := appendSemanticPredicates(descriptor, plan, condition, semanticPath(path, "AND", position)); err != nil {
				return nil, err
			}
		}
	}
	for index, column := range plan.Select {
		projection := SemanticQueryProjection{
			Alias: column.Alias, OutputPointer: semanticOutputPointer(column.Alias),
		}
		switch {
		case column.Graph != nil:
			projection.Value = semanticGraphKind(column.Graph)
			projection.PropertyRefs, projection.RelationRefs = semanticGraphRefs(plan, column.Graph)
		case column.Expression != nil:
			projection.Expression = semanticExpressionKind(column.Expression)
			projection.PropertyRefs = semanticExpressionRefs(plan, column.Expression)
		case column.Aggregate != nil:
			projection.Aggregate = strings.ToLower(column.Aggregate.Function)
			projection.Distinct = column.Aggregate.Distinct
			switch {
			case column.Aggregate.Argument != nil:
				projection.Expression = semanticExpression
				projection.PropertyRefs = semanticExpressionRefs(plan, column.Aggregate.Argument)
			case len(column.Aggregate.Keys) > 0:
				// count(DISTINCT n) over a node keyed by several properties
				// reads every one of them.
				projection.PropertyRefs = semanticExpressionRefs(plan, *column.Aggregate)
			case !column.Aggregate.Star:
				projection.PropertyRef = semanticPropertyRef(plan, column.Aggregate.Table, column.Aggregate.Property)
			}
		default:
			projection.PropertyRef = semanticPropertyRef(plan, column.Table, column.Property)
		}
		if projection.PropertyRef == "" && projection.Aggregate == "" && projection.Expression == "" && projection.Value == "" {
			return nil, fmt.Errorf("semantic query projection %d is incomplete", index)
		}
		descriptor.Projections = append(descriptor.Projections, projection)
	}
	if len(plan.Totals) > 0 {
		descriptor.Projections = semanticTotals(plan.Totals, descriptor.Projections)
	}
	for _, column := range plan.GroupBy {
		if column.Graph != nil {
			refs, _ := semanticGraphRefs(plan, column.Graph)
			descriptor.Grouping = append(descriptor.Grouping, refs...)
			continue
		}
		if column.Expression != nil {
			descriptor.Grouping = append(descriptor.Grouping, semanticExpressionRefs(plan, column.Expression)...)
			continue
		}
		if ref := semanticPropertyRef(plan, column.Table, column.Property); ref != "" {
			descriptor.Grouping = append(descriptor.Grouping, ref)
		}
	}
	for _, order := range plan.OrderBy {
		item := SemanticQueryOrdering{Alias: order.Alias, Descending: order.Descending}
		switch {
		case order.Aggregate != nil:
			item.Aggregate = strings.ToLower(order.Aggregate.Function)
			switch {
			case order.Aggregate.Argument != nil:
				item.Expression = semanticExpression
				item.PropertyRefs = semanticExpressionRefs(plan, order.Aggregate.Argument)
			case len(order.Aggregate.Keys) > 0:
				// count(DISTINCT n) over a node keyed by several properties
				// reads every one of them.
				item.PropertyRefs = semanticExpressionRefs(plan, *order.Aggregate)
			case !order.Aggregate.Star:
				item.PropertyRef = semanticPropertyRef(plan, order.Aggregate.Table, order.Aggregate.Property)
			}
		case order.Expression != nil:
			item.Expression = semanticExpression
			item.PropertyRefs = semanticExpressionRefs(plan, order.Expression)
		case order.Property != "":
			item.PropertyRef = semanticPropertyRef(plan, order.Table, order.Property)
		}
		descriptor.Ordering = append(descriptor.Ordering, item)
	}
	if plan.Carried != nil {
		if err := appendSemanticStage(descriptor, plan.Carried.Stage, "carried."); err != nil {
			return nil, err
		}
	}
	raw, err := json.Marshal(descriptor)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxSemanticDescriptorBytes {
		return nil, fmt.Errorf("%w: exceeds %d bytes", ErrSemanticDescriptorTooLarge, maxSemanticDescriptorBytes)
	}
	return descriptor, nil
}

func appendSemanticPredicates(descriptor *SemanticQueryDescriptor, plan *Plan, predicate PlanPredicate, path string) error {
	switch value := predicate.(type) {
	case nil, PlanNever:
		return nil
	case PlanSemiJoin:
		return appendSemanticSemiJoin(descriptor, plan, value, path)
	case PlanColumnComparison:
		// The planner's own comparisons, which keep two hops apart, are not
		// something the query asked for.
		if value.LeftProperty == "" || value.RightProperty == "" {
			return nil
		}
		refs := []string{
			semanticPropertyRef(plan, value.LeftTable, value.LeftProperty),
			semanticPropertyRef(plan, value.RightTable, value.RightProperty),
		}
		descriptor.Predicates = append(descriptor.Predicates, SemanticQueryPredicate{
			Operator: value.Operator, Expression: semanticComparison, PropertyRefs: dedupe(refs),
			InputPointer: "$.query", LogicalPath: path,
		})
	case PlanCondition:
		ref := semanticPropertyRef(plan, value.Table, value.Property)
		if ref == "" {
			return errors.New("semantic query condition is missing its bound property")
		}
		descriptor.Predicates = append(descriptor.Predicates, SemanticQueryPredicate{
			PropertyRef: ref, Operator: value.Operator, InputPointer: value.InputPointer,
			ValueHashes: []string{semanticLiteralHash(value.Value)}, LogicalPath: path,
		})
	case PlanNullCheck:
		operator := "is_null"
		if value.Negated {
			operator = "is_not_null"
		}
		descriptor.Predicates = append(descriptor.Predicates, SemanticQueryPredicate{
			PropertyRef: semanticPropertyRef(plan, value.Table, value.Property),
			Operator:    operator, InputPointer: "$.query", LogicalPath: path,
		})
	case PlanMembership:
		operator := "in"
		if value.Negated {
			operator = "not_in"
		}
		hashes := make([]string, 0, len(value.Values))
		for _, literal := range value.Values {
			hashes = append(hashes, semanticLiteralHash(literal))
		}
		pointer := "$.query"
		switch {
		case value.ListInputPointer != "":
			pointer = value.ListInputPointer
		case len(value.InputPointers) == 1:
			pointer = value.InputPointers[0]
		}
		descriptor.Predicates = append(descriptor.Predicates, SemanticQueryPredicate{
			PropertyRef: semanticPropertyRef(plan, value.Table, value.Property),
			Operator:    operator, InputPointer: pointer, ValueHashes: hashes, LogicalPath: path,
		})
	case PlanExpressionComparison:
		refs := append(semanticExpressionRefs(plan, value.Left), semanticExpressionRefs(plan, value.Right)...)
		hashes, pointers := semanticExpressionValues(value.Left)
		rightHashes, rightPointers := semanticExpressionValues(value.Right)
		hashes, pointers = append(hashes, rightHashes...), append(pointers, rightPointers...)
		pointer := "$.query"
		for _, candidate := range pointers {
			if candidate != "$.query" {
				// One parameter names where the value came from; several do
				// not fit one pointer, and the query is where they meet.
				if pointer != "$.query" && pointer != candidate {
					pointer = "$.query"
					break
				}
				pointer = candidate
			}
		}
		descriptor.Predicates = append(descriptor.Predicates, SemanticQueryPredicate{
			Operator: value.Operator, Expression: semanticExpression, PropertyRefs: dedupe(refs),
			InputPointer: pointer, ValueHashes: hashes, LogicalPath: path,
		})
	case PlanStringMatch:
		operator, ok := semanticStringMatchOperators[value.Operator]
		if !ok {
			return fmt.Errorf("unsupported semantic string predicate %q", value.Operator)
		}
		descriptor.Predicates = append(descriptor.Predicates, SemanticQueryPredicate{
			PropertyRef: semanticPropertyRef(plan, value.Table, value.Property),
			Operator:    operator, InputPointer: value.InputPointer,
			ValueHashes: []string{semanticLiteralHash(Literal{Kind: LiteralString, String: value.Value})},
			LogicalPath: path,
		})
	case PlanNegation:
		return appendSemanticPredicates(descriptor, plan, value.Operand, semanticPath(path, "NOT", 0))
	case PlanLogical:
		for index, operand := range value.Operands {
			if err := appendSemanticPredicates(descriptor, plan, operand, semanticPath(path, value.Operator, index)); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unsupported semantic predicate %T", predicate)
	}
	return nil
}

func semanticPropertyRef(plan *Plan, table int, property string) string {
	if plan == nil || table < 0 || table >= len(plan.Tables) || property == "" || plan.Tables[table].ObjectTypeID == "" {
		return ""
	}
	return "property:" + plan.NetworkID + ":" + plan.Tables[table].ObjectTypeID + ":" + property
}

// semanticExpressionRefs lists the properties a computed value reads, each
// once, in the order it reads them.
func semanticExpressionRefs(plan *Plan, value PlanExpression) []string {
	var refs []string
	seen := map[string]bool{}
	add := func(ref string) {
		if ref != "" && !seen[ref] {
			seen[ref] = true
			refs = append(refs, ref)
		}
	}
	var walk func(PlanExpression)
	var walkPredicate func(PlanPredicate)
	walkPredicate = func(predicate PlanPredicate) {
		switch node := predicate.(type) {
		case PlanCondition:
			add(semanticPropertyRef(plan, node.Table, node.Property))
		case PlanNullCheck:
			add(semanticPropertyRef(plan, node.Table, node.Property))
		case PlanMembership:
			add(semanticPropertyRef(plan, node.Table, node.Property))
		case PlanStringMatch:
			add(semanticPropertyRef(plan, node.Table, node.Property))
		case PlanExpressionComparison:
			walk(node.Left)
			walk(node.Right)
		case PlanColumnComparison:
			add(semanticPropertyRef(plan, node.LeftTable, node.LeftProperty))
			add(semanticPropertyRef(plan, node.RightTable, node.RightProperty))
		case PlanNegation:
			walkPredicate(node.Operand)
		case PlanLogical:
			for _, operand := range node.Operands {
				walkPredicate(operand)
			}
		case dateComparison:
			walkPredicate(node.PlanPredicate)
		}
	}
	walk = func(value PlanExpression) {
		var ref string
		switch node := value.(type) {
		case PlanConditionValue:
			walkPredicate(node.Predicate)
		case PlanValueColumn:
			ref = semanticPropertyRef(plan, node.Table, node.Property)
		case PlanAggregate:
			if node.Argument != nil {
				walk(node.Argument)
			} else if !node.Star {
				add(semanticPropertyRef(plan, node.Table, node.Property))
				for _, key := range node.Keys {
					add(semanticPropertyRef(plan, node.Table, key.Property))
				}
			}
		case PlanArithmetic:
			walk(node.Left)
			walk(node.Right)
		case PlanNegative:
			walk(node.Operand)
		case PlanCase, PlanCoalesce:
			for _, condition := range caseConditions(value) {
				walkPredicate(condition)
			}
			for _, part := range caseValues(value) {
				walk(part)
			}
		}
		if ref != "" && !seen[ref] {
			seen[ref] = true
			refs = append(refs, ref)
		}
	}
	walk(value)
	return refs
}

// semanticExpressionValues hashes the numbers a computed value carries, in
// order, and says where each came from. The numbers themselves are not
// recorded, as they are not for any other condition.
func semanticExpressionValues(value PlanExpression) ([]string, []string) {
	switch node := value.(type) {
	case PlanValueLiteral:
		return []string{semanticLiteralHash(node.Value)}, []string{node.InputPointer}
	case PlanAggregate:
		if node.Argument != nil {
			return semanticExpressionValues(node.Argument)
		}
	case PlanArithmetic:
		leftHashes, leftPointers := semanticExpressionValues(node.Left)
		rightHashes, rightPointers := semanticExpressionValues(node.Right)
		return append(leftHashes, rightHashes...), append(leftPointers, rightPointers...)
	case PlanNegative:
		return semanticExpressionValues(node.Operand)
	case PlanCase, PlanCoalesce:
		// A branch's condition records its values as any condition does;
		// what the branches give is hashed here.
		var hashes, pointers []string
		for _, part := range caseValues(value) {
			partHashes, partPointers := semanticExpressionValues(part)
			hashes, pointers = append(hashes, partHashes...), append(pointers, partPointers...)
		}
		return hashes, pointers
	}
	return nil, nil
}

func dedupe(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := values[:0:0]
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func semanticAlias(table PlanTable, index int) string {
	if table.Variable != "" {
		return table.Variable
	}
	return fmt.Sprintf("_%d", index)
}

func semanticOutputPointer(alias string) string {
	if simpleJSONPathField.MatchString(alias) {
		return "$.entries[*]." + alias
	}
	return "$.entries[*][" + strconv.Quote(alias) + "]"
}

func semanticPath(parent, operator string, index int) string {
	part := fmt.Sprintf("%s[%d]", strings.ToUpper(operator), index)
	if parent == "" {
		return part
	}
	return parent + "." + part
}

func semanticQueryHash(query string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(query)))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func semanticLiteralHash(value Literal) string {
	var semantic any
	switch value.Kind {
	case LiteralString:
		semantic = value.String
	case LiteralInteger:
		semantic = value.Integer
	case LiteralFloat:
		semantic = value.Float
	case LiteralBoolean:
		semantic = value.Boolean
	default:
		semantic = nil
	}
	raw, _ := json.Marshal(semantic)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// appendSemanticSemiJoin records a semi-join: the condition itself, reading
// the outer property and the earlier stage's value, and what that stage read.
// The stage's own aliases are prefixed, since a stage may reuse a name the
// query uses for something else.
func appendSemanticSemiJoin(descriptor *SemanticQueryDescriptor, plan *Plan, join PlanSemiJoin, path string) error {
	sub, err := BuildSemanticQueryDescriptor(join.Sub, "")
	if err != nil {
		return err
	}
	stage := fmt.Sprintf("stage%d.", len(descriptor.Predicates))
	inner := ""
	if len(sub.Projections) > 0 {
		inner = sub.Projections[0].PropertyRef
	}
	descriptor.Predicates = append(descriptor.Predicates, SemanticQueryPredicate{
		Operator: "in", Expression: semanticSemiJoin, InputPointer: "$.query", LogicalPath: path,
		PropertyRefs: dedupe([]string{semanticPropertyRef(plan, join.Table, join.Property), inner}),
	})
	for _, object := range sub.Objects {
		object.Alias = stage + object.Alias
		descriptor.Objects = append(descriptor.Objects, object)
	}
	for _, relation := range sub.Relations {
		relation.SourceAlias, relation.TargetAlias = stage+relation.SourceAlias, stage+relation.TargetAlias
		descriptor.Relations = append(descriptor.Relations, relation)
	}
	for _, predicate := range sub.Predicates {
		predicate.LogicalPath = strings.TrimSuffix(stage+predicate.LogicalPath, ".")
		descriptor.Predicates = append(descriptor.Predicates, predicate)
	}
	return nil
}

// semanticGraphKind names what a graph value is.
func semanticGraphKind(value PlanGraph) string {
	switch value.(type) {
	case PlanNodeValue:
		return "node"
	case PlanRelationValue:
		return "relation"
	case PlanGraphConstant:
		return "constant"
	case PlanGraphProperty:
		return "property"
	case PlanGraphMap:
		return "map"
	case PlanGraphCollect:
		return "collect"
	default:
		return "graph"
	}
}

// semanticGraphRefs lists, each once, the properties a graph value reads and
// the relation types it returns, in the order first met.
func semanticGraphRefs(plan *Plan, value PlanGraph) ([]string, []string) {
	var properties, relations []string
	seen := map[string]bool{}
	add := func(list *[]string, ref string) {
		if ref != "" && !seen[ref] {
			seen[ref] = true
			*list = append(*list, ref)
		}
	}
	identity := func(id PlanIdentity) {
		for _, key := range id.Keys {
			add(&properties, semanticPropertyRef(plan, id.Table, key.Property))
		}
	}
	var walk func(PlanGraph)
	walk = func(value PlanGraph) {
		switch node := value.(type) {
		case PlanNodeValue:
			identity(node.Identity)
			for _, property := range node.Snapshot {
				add(&properties, semanticPropertyRef(plan, node.Table, property.Property))
			}
		case PlanRelationValue:
			add(&relations, "relation:"+plan.NetworkID+":"+node.RelationTypeID)
			identity(node.Source)
			identity(node.Target)
		case PlanGraphProperty:
			add(&properties, semanticPropertyRef(plan, node.Table, node.Property))
		case PlanGraphMap:
			for _, field := range node.Values {
				walk(field)
			}
		case PlanGraphCollect:
			walk(node.Argument)
		}
	}
	walk(value)
	return properties, relations
}

// semanticIntersectionDescriptor describes a query comparing two collected
// lists: what each list's stage reads, under the alias prefixes list0. and
// list1., and the sizes returned, each a count over the property its list
// collects.
func semanticIntersectionDescriptor(plan *Plan, query string) (*SemanticQueryDescriptor, error) {
	if strings.TrimSpace(plan.NetworkID) == "" || len(plan.Intersection.Items) == 0 {
		return nil, errors.New("semantic query descriptor requires a bound query plan")
	}
	descriptor := &SemanticQueryDescriptor{
		Version:         "semantic-query-descriptor/v1",
		ProducerProfile: "openbkn.bkn-backend.run_cypher@0.1.5",
		NetworkID:       plan.NetworkID, Branch: plan.Branch,
		QueryHash:      semanticQueryHash(query),
		Objects:        []SemanticQueryObject{},
		Projections:    make([]SemanticQueryProjection, 0, len(plan.Intersection.Items)),
		EffectiveLimit: int64(interfaces.CYPHER_DEFAULT_LIMIT), LimitSource: "default",
		ResultPointer: "$.entries",
	}
	var values [2]string
	for i, list := range plan.Intersection.Lists {
		sub, err := BuildSemanticQueryDescriptor(list, "")
		if err != nil {
			return nil, err
		}
		stage := fmt.Sprintf("list%d.", i)
		for _, object := range sub.Objects {
			object.Alias = stage + object.Alias
			descriptor.Objects = append(descriptor.Objects, object)
		}
		for _, relation := range sub.Relations {
			relation.SourceAlias, relation.TargetAlias = stage+relation.SourceAlias, stage+relation.TargetAlias
			descriptor.Relations = append(descriptor.Relations, relation)
		}
		for _, predicate := range sub.Predicates {
			predicate.LogicalPath = strings.TrimSuffix(stage+predicate.LogicalPath, ".")
			descriptor.Predicates = append(descriptor.Predicates, predicate)
		}
		if len(sub.Projections) > 0 {
			values[i] = sub.Projections[0].PropertyRef
		}
	}
	for _, item := range plan.Intersection.Items {
		projection := SemanticQueryProjection{
			Alias: item.Alias, Aggregate: "count", Distinct: plan.Intersection.Distinct[item.List],
			OutputPointer: semanticOutputPointer(item.Alias),
		}
		if item.Common {
			projection.Expression = semanticIntersection
			projection.PropertyRefs = dedupe([]string{values[item.List], values[1-item.List]})
		} else {
			projection.PropertyRef = values[item.List]
		}
		descriptor.Projections = append(descriptor.Projections, projection)
	}
	raw, err := json.Marshal(descriptor)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxSemanticDescriptorBytes {
		return nil, fmt.Errorf("%w: exceeds %d bytes", ErrSemanticDescriptorTooLarge, maxSemanticDescriptorBytes)
	}
	return descriptor, nil
}

// appendSemanticStage records what a stage the statement joins reads, under
// an alias prefix that keeps its objects apart from the outer pattern's.
func appendSemanticStage(descriptor *SemanticQueryDescriptor, stage *Plan, prefix string) error {
	sub, err := BuildSemanticQueryDescriptor(stage, "")
	if err != nil {
		return err
	}
	for _, object := range sub.Objects {
		object.Alias = prefix + object.Alias
		descriptor.Objects = append(descriptor.Objects, object)
	}
	for _, relation := range sub.Relations {
		relation.SourceAlias, relation.TargetAlias = prefix+relation.SourceAlias, prefix+relation.TargetAlias
		descriptor.Relations = append(descriptor.Relations, relation)
	}
	for _, predicate := range sub.Predicates {
		predicate.LogicalPath = strings.TrimSuffix(prefix+predicate.LogicalPath, ".")
		descriptor.Predicates = append(descriptor.Predicates, predicate)
	}
	return nil
}
