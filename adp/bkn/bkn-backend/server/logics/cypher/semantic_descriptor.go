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

	"bkn-backend/interfaces"
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
}

type SemanticQueryPredicate struct {
	PropertyRef  string   `json:"property_ref"`
	Operator     string   `json:"operator"`
	InputPointer string   `json:"input_pointer"`
	ValueHashes  []string `json:"value_hashes,omitempty"`
	LogicalPath  string   `json:"logical_path,omitempty"`
}

type SemanticQueryProjection struct {
	Alias         string `json:"alias"`
	PropertyRef   string `json:"property_ref,omitempty"`
	Aggregate     string `json:"aggregate,omitempty"`
	Distinct      bool   `json:"distinct,omitempty"`
	OutputPointer string `json:"output_pointer"`
}

type SemanticQueryOrdering struct {
	Alias       string `json:"alias,omitempty"`
	PropertyRef string `json:"property_ref,omitempty"`
	Aggregate   string `json:"aggregate,omitempty"`
	Descending  bool   `json:"descending"`
}

func BuildSemanticQueryDescriptor(plan *Plan, query string) (*SemanticQueryDescriptor, error) {
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
		})
	}
	if err := appendSemanticPredicates(descriptor, plan, plan.Where, ""); err != nil {
		return nil, err
	}
	for index, column := range plan.Select {
		projection := SemanticQueryProjection{
			Alias: column.Alias, OutputPointer: semanticOutputPointer(column.Alias),
		}
		if column.Aggregate != nil {
			projection.Aggregate = strings.ToLower(column.Aggregate.Function)
			projection.Distinct = column.Aggregate.Distinct
			if !column.Aggregate.Star {
				projection.PropertyRef = semanticPropertyRef(plan, column.Aggregate.Table, column.Aggregate.Property)
			}
		} else {
			projection.PropertyRef = semanticPropertyRef(plan, column.Table, column.Property)
		}
		if projection.PropertyRef == "" && projection.Aggregate == "" {
			return nil, fmt.Errorf("semantic query projection %d is incomplete", index)
		}
		descriptor.Projections = append(descriptor.Projections, projection)
	}
	for _, column := range plan.GroupBy {
		if ref := semanticPropertyRef(plan, column.Table, column.Property); ref != "" {
			descriptor.Grouping = append(descriptor.Grouping, ref)
		}
	}
	for _, order := range plan.OrderBy {
		item := SemanticQueryOrdering{Alias: order.Alias, Descending: order.Descending}
		switch {
		case order.Aggregate != nil:
			item.Aggregate = strings.ToLower(order.Aggregate.Function)
			if !order.Aggregate.Star {
				item.PropertyRef = semanticPropertyRef(plan, order.Aggregate.Table, order.Aggregate.Property)
			}
		case order.Property != "":
			item.PropertyRef = semanticPropertyRef(plan, order.Table, order.Property)
		}
		descriptor.Ordering = append(descriptor.Ordering, item)
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
	case nil, PlanNever, PlanColumnComparison:
		return nil
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
