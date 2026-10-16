// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"encoding/json"
	"fmt"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// SemanticGraphExpression records logical values without exposing physical
// column aliases, mapping fields, or literal values in the trace.
type SemanticGraphExpression struct {
	Aggregate    string                              `json:"aggregate,omitempty"`
	Distinct     bool                                `json:"distinct,omitempty"`
	Binding      string                              `json:"binding,omitempty"`
	Operation    string                              `json:"operation,omitempty"`
	Arguments    []*SemanticGraphExpression          `json:"arguments,omitempty"`
	InputPointer string                              `json:"input_pointer,omitempty"`
	Type         *interfaces.CypherValueType         `json:"type"`
	PropertyRefs []string                            `json:"property_refs,omitempty"`
	ObjectRef    string                              `json:"object_ref,omitempty"`
	RelationRef  string                              `json:"relation_ref,omitempty"`
	LiteralHash  string                              `json:"literal_hash,omitempty"`
	Fields       map[string]*SemanticGraphExpression `json:"fields,omitempty"`
	Element      *SemanticGraphExpression            `json:"element,omitempty"`
}

func describeGraphResult(descriptor *SemanticQueryDescriptor, plan *Plan) {
	if plan.Result.PostWhere != nil {
		descriptor.PostPredicate = describeGraphExpression(plan, plan.Result.PostWhere)
	}
	for _, unwind := range plan.Result.Unwinds {
		descriptor.Unwinds = append(descriptor.Unwinds, SemanticQueryUnwind{Variable: unwind.Variable, Expression: describeGraphExpression(plan, unwind.Value)})
	}
	descriptor.GraphMatchBudget = graphMatchBudget
	if plan.Result.Where != nil {
		descriptor.ResidualPredicate = describeGraphExpression(plan, plan.Result.Where)
	}
	descriptor.Limit = nil
	if plan.Result.LimitExplicit {
		descriptor.Limit = cloneInt64(&plan.Result.Limit)
	}
	descriptor.EffectiveLimit = plan.Result.Limit
	descriptor.LimitSource = "graph_result"
	descriptor.Projections = []SemanticQueryProjection{}
	descriptor.Grouping = nil
	if plan.Result.Skip > 0 {
		descriptor.Skip = cloneInt64(&plan.Result.Skip)
	}
	for _, order := range plan.Result.OrderBy {
		item := SemanticQueryOrdering{Alias: order.Alias, Descending: order.Descending}
		if order.Value != nil {
			item.Expression = describeGraphExpression(plan, order.Value)
		}
		descriptor.Ordering = append(descriptor.Ordering, item)
	}
	for _, projection := range plan.Result.Projections {
		item := SemanticQueryProjection{Alias: projection.Alias, OutputPointer: semanticOutputPointer(projection.Alias), Expression: describeGraphExpression(plan, projection.Value)}
		if projection.Value.Element != nil {
			item.Aggregate = projection.Value.Aggregate
			if item.Aggregate == "" {
				item.Aggregate = "collect"
			}
			item.Distinct = projection.Value.Distinct
		} else if plan.Result.HasAggregates && !containsGraphAggregate(projection.Value) {
			descriptor.Grouping = append(descriptor.Grouping, projection.Alias)
		}
		descriptor.Projections = append(descriptor.Projections, item)
	}
}

func describeGraphExpression(plan *Plan, expression *GraphExpression) *SemanticGraphExpression {
	result := &SemanticGraphExpression{Aggregate: expression.Aggregate, Distinct: expression.Distinct, Type: expression.Type, Operation: expression.Operation, InputPointer: expression.InputPointer, Binding: expression.Binding, PropertyRefs: append([]string(nil), expression.PropertyRefs...)}
	if subquery := expression.Subquery; subquery != nil {
		result.Operation = "subquery:" + subquery.Kind
		return result
	}
	if collection := expression.Collection; collection != nil {
		result.Operation = "collection:" + collection.Kind
		result.Binding = collection.Variable
		result.Fields = map[string]*SemanticGraphExpression{"source": describeGraphExpression(plan, collection.Source), "projection": describeGraphExpression(plan, collection.Projection)}
		if collection.Predicate != nil {
			result.Fields["predicate"] = describeGraphExpression(plan, collection.Predicate)
		}
		return result
	}
	if path := expression.Path; path != nil {
		result.Operation = "path"
		result.Fields = map[string]*SemanticGraphExpression{}
		for name, values := range map[string][]*GraphExpression{"nodes": path.Nodes, "relationships": path.Relationships} {
			items := &SemanticGraphExpression{Operation: "LIST", Type: &interfaces.CypherValueType{Kind: "list"}}
			for _, value := range values {
				items.Arguments = append(items.Arguments, describeGraphExpression(plan, value))
			}
			result.Fields[name] = items
		}
		return result
	}
	for _, argument := range expression.Arguments {
		result.Arguments = append(result.Arguments, describeGraphExpression(plan, argument))
	}
	switch {
	case expression.Binding != "":
		return result
	case expression.Operation != "":
		return result
	case expression.Column != "":
		result.PropertyRefs = []string{expression.PropertyRef}
	case expression.Object != nil:
		result.ObjectRef = "object:" + plan.NetworkID + ":" + expression.Object.TypeID
		result.PropertyRefs = graphObjectPropertyRefs(plan, expression.Object)
	case expression.Relation != nil:
		result.RelationRef = "relation:" + plan.NetworkID + ":" + expression.Relation.TypeID
		result.PropertyRefs = append(graphObjectPropertyRefs(plan, expression.Relation.Source), graphObjectPropertyRefs(plan, expression.Relation.Target)...)
		for _, key := range expression.Relation.Basis {
			result.PropertyRefs = append(result.PropertyRefs, "property:"+plan.NetworkID+":"+expression.Relation.Source.TypeID+":"+key.SourceProperty, "property:"+plan.NetworkID+":"+expression.Relation.Target.TypeID+":"+key.TargetProperty)
		}
	case expression.Fields != nil:
		result.Fields = map[string]*SemanticGraphExpression{}
		for name, field := range expression.Fields {
			result.Fields[name] = describeGraphExpression(plan, field)
		}
	case expression.Element != nil:
		result.Element = describeGraphExpression(plan, expression.Element)
	default:
		raw, _ := json.Marshal(expression.Literal)
		result.LiteralHash = semanticQueryHash(string(raw))
	}
	return result
}

func graphObjectPropertyRefs(plan *Plan, object *GraphObjectPlan) []string {
	fields := map[string]bool{}
	for name := range object.Keys {
		fields[name] = true
	}
	for name := range object.Properties {
		fields[name] = true
	}
	for name := range object.InternalProperties {
		fields[name] = true
	}
	refs := make([]string, 0, len(fields))
	for _, name := range sortedGraphKeys(fields) {
		refs = append(refs, fmt.Sprintf("property:%s:%s:%s", plan.NetworkID, object.TypeID, name))
	}
	return refs
}
