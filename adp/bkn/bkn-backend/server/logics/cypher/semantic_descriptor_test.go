// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package cypher

import (
	"strings"
	"testing"
)

func TestSemanticQueryDescriptorPreservesBoundOntologyMeaning(t *testing.T) {
	tree, err := Parse(`MATCH (o:Order)-[:PLACED_BY]->(c:Customer)
		WHERE o.amount >= $minimum
		RETURN c.region AS region, count(DISTINCT o.id) AS orders
		ORDER BY orders DESC SKIP 2 LIMIT 5`)
	if err != nil {
		t.Fatal(err)
	}
	query, err := Analyze(tree)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Compile(query, modelSchema(t), CompileOptions{Parameters: map[string]any{"minimum": 100}})
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := BuildSemanticQueryDescriptor(plan, tree.GetText())
	if err != nil {
		t.Fatal(err)
	}

	if descriptor.Version != "semantic-query-descriptor/v1" ||
		descriptor.ProducerProfile != "openbkn.bkn-backend.run_cypher@0.1.5" ||
		descriptor.NetworkID != "kn_test" || descriptor.Branch != "main" ||
		!strings.HasPrefix(descriptor.QueryHash, "sha256:") {
		t.Fatalf("descriptor identity = %+v", descriptor)
	}
	if len(descriptor.Objects) != 2 ||
		descriptor.Objects[0].Alias != "o" || descriptor.Objects[0].ObjectRef != "object:kn_test:ot_order" ||
		descriptor.Objects[1].Alias != "c" || descriptor.Objects[1].ObjectRef != "object:kn_test:ot_customer" {
		t.Fatalf("descriptor objects = %+v", descriptor.Objects)
	}
	if len(descriptor.Relations) != 1 || descriptor.Relations[0].RelationRef != "relation:kn_test:rt_placed_by" ||
		descriptor.Relations[0].SourceAlias != "o" || descriptor.Relations[0].TargetAlias != "c" ||
		descriptor.Relations[0].Direction != "outgoing" {
		t.Fatalf("descriptor relations = %+v", descriptor.Relations)
	}
	if len(descriptor.Predicates) != 1 ||
		descriptor.Predicates[0].PropertyRef != "property:kn_test:ot_order:amount" ||
		descriptor.Predicates[0].Operator != ">=" ||
		descriptor.Predicates[0].InputPointer != "$.parameters.minimum" {
		t.Fatalf("descriptor predicates = %+v", descriptor.Predicates)
	}
	if len(descriptor.Projections) != 2 ||
		descriptor.Projections[0].PropertyRef != "property:kn_test:ot_customer:region" ||
		descriptor.Projections[0].OutputPointer != "$.entries[*].region" ||
		descriptor.Projections[1].Aggregate != "count" || !descriptor.Projections[1].Distinct ||
		descriptor.Projections[1].PropertyRef != "property:kn_test:ot_order:id" ||
		descriptor.Projections[1].OutputPointer != "$.entries[*].orders" {
		t.Fatalf("descriptor projections = %+v", descriptor.Projections)
	}
	if len(descriptor.Grouping) != 1 || descriptor.Grouping[0] != "property:kn_test:ot_customer:region" ||
		len(descriptor.Ordering) != 1 || descriptor.Ordering[0].Alias != "orders" || !descriptor.Ordering[0].Descending ||
		descriptor.Skip == nil || *descriptor.Skip != 2 || descriptor.Limit == nil || *descriptor.Limit != 5 ||
		descriptor.EffectiveLimit != 5 || descriptor.LimitSource != "explicit" ||
		descriptor.ResultPointer != "$.entries" {
		t.Fatalf("descriptor result plan = %+v", descriptor)
	}
}

func TestSemanticQueryDescriptorRecordsTheDefaultResultBoundary(t *testing.T) {
	plan := &Plan{
		NetworkID: "kn_test", Branch: "main",
		Tables: []PlanTable{{Variable: "o", ObjectTypeID: "ot_order"}},
		Select: []PlanColumn{{Alias: "id", Table: 0, Property: "id"}},
	}
	descriptor, err := BuildSemanticQueryDescriptor(plan, "MATCH (o:Order) RETURN o.id AS id")
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.Limit != nil || descriptor.EffectiveLimit != 1000 || descriptor.LimitSource != "default" {
		t.Fatalf("descriptor result boundary = %+v", descriptor)
	}
}

func TestSemanticQueryDescriptorIsBoundedAndRejectsIncompleteMeaning(t *testing.T) {
	if _, err := BuildSemanticQueryDescriptor(&Plan{}, "MATCH (n:X) RETURN n.id"); err == nil {
		t.Fatal("descriptor without a knowledge-network scope was accepted")
	}
	plan := &Plan{NetworkID: "kn", Branch: "main", Tables: []PlanTable{{
		Variable: "n", ObjectTypeID: strings.Repeat("x", maxSemanticDescriptorBytes),
	}}}
	if _, err := BuildSemanticQueryDescriptor(plan, "MATCH (n:X) RETURN n.id"); err == nil {
		t.Fatal("oversized descriptor was accepted")
	}
}
