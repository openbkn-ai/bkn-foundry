// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledge_network

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common"
	cond "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common/condition"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

// Regression for #3029: a null or missing mapped value never joins, like SQL
// equality, and must not reach a downstream filter, which rejects it.

// nullRejectingVega serves backing rows and fails a filter carrying a null,
// as the resource query does.
type nullRejectingVega struct {
	backingTableVega
}

func (v *nullRejectingVega) QueryResourceData(ctx context.Context, id string,
	params *interfaces.ResourceDataQueryParams) (*interfaces.DatasetQueryResponse, error) {
	if filterHasNull(params.FilterCondition) {
		v.history = append(v.history, params)
		return nil, errors.New("resource query failed: null in filter")
	}
	return v.backingTableVega.QueryResourceData(ctx, id, params)
}

// filterHasNull walks the filter_condition JSON sent to Vega. A nil value is
// dropped from the JSON, so an equality without a value carries a null.
func filterHasNull(filter map[string]any) bool {
	if filter == nil {
		return false
	}
	switch filter["operation"] {
	case cond.OperationAnd, cond.OperationOr:
		subConditions, _ := filter["sub_conditions"].([]any)
		for _, sub := range subConditions {
			if filterHasNull(sub.(map[string]any)) {
				return true
			}
		}
		return false
	case "in":
		values, _ := filter["value"].([]any)
		for _, value := range values {
			if value == nil {
				return true
			}
		}
		return false
	}
	value, exists := filter["value"]
	return !exists || value == nil
}

// nullRejectingObjects serves object rows by type and fails a condition
// carrying a null, as the object resource query does.
type nullRejectingObjects struct {
	customerObjects
	rows map[string][]map[string]any
}

func (o *nullRejectingObjects) GetObjectsByObjectTypeID(_ context.Context,
	query *interfaces.ObjectQueryBaseOnObjectType) (interfaces.Objects, error) {
	o.queries = append(o.queries, query)
	if conditionHasNull(query.ActualCondition) {
		return interfaces.Objects{}, errors.New("object resource query failed: null in condition")
	}
	objects := interfaces.Objects{ObjectType: &interfaces.ObjectType{
		ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: query.ObjectTypeID, PrimaryKeys: []string{"id"}}}}
	for _, row := range o.rows[query.ObjectTypeID] {
		if rowMatchesCondition(query.ActualCondition, row) {
			objects.Datas = append(objects.Datas, row)
		}
	}
	return objects, nil
}

func conditionHasNull(condition *cond.CondCfg) bool {
	if condition == nil {
		return false
	}
	switch condition.Operation {
	case cond.OperationAnd, cond.OperationOr:
		for _, sub := range condition.SubConds {
			if conditionHasNull(sub) {
				return true
			}
		}
		return false
	case "in":
		for _, value := range condition.Value.([]any) {
			if value == nil {
				return true
			}
		}
		return false
	}
	return condition.Value == nil
}

func rowMatchesCondition(condition *cond.CondCfg, row map[string]any) bool {
	if condition == nil {
		return true
	}
	switch condition.Operation {
	case cond.OperationAnd, cond.OperationOr:
		isAnd := condition.Operation == cond.OperationAnd
		for _, sub := range condition.SubConds {
			if rowMatchesCondition(sub, row) != isAnd {
				return !isAnd
			}
		}
		return isAnd
	case "in":
		for _, value := range condition.Value.([]any) {
			if actual, exists := row[condition.Name]; exists && fmt.Sprint(actual) == fmt.Sprint(value) {
				return true
			}
		}
		return false
	case "==":
		actual, exists := row[condition.Name]
		return exists && fmt.Sprint(actual) == fmt.Sprint(condition.Value)
	}
	panic(fmt.Sprintf("unsupported condition %s", condition.Operation))
}

func nullKeyRule(source, target string) interfaces.Mapping {
	return interfaces.Mapping{SourceProp: interfaces.SimpleProperty{Name: source},
		TargetProp: interfaces.SimpleProperty{Name: target}}
}

// nullKeyObject is an object row with an instance id; props may hold nils or
// omit a field.
func nullKeyObject(objectTypeID, id string, props map[string]any) map[string]any {
	row := map[string]any{"id": id, interfaces.SYSTEM_PROPERTY_INSTANCE_ID: objectTypeID + "-" + id}
	for key, value := range props {
		row[key] = value
	}
	return row
}

func nullKeyLevelObjects(rows ...map[string]any) []interfaces.LevelObject {
	objects := make([]interfaces.LevelObject, len(rows))
	for index, row := range rows {
		objects[index] = interfaces.LevelObject{ObjectID: row[interfaces.SYSTEM_PROPERTY_INSTANCE_ID].(string), ObjectData: row}
	}
	return objects
}

func reachedInstances(result map[string]interfaces.Objects) map[string][]string {
	reached := map[string][]string{}
	for objectID, objects := range result {
		for _, object := range objects.Datas {
			reached[objectID] = append(reached[objectID], object[interfaces.SYSTEM_PROPERTY_INSTANCE_ID].(string))
		}
		sort.Strings(reached[objectID])
	}
	return reached
}

type nullKeyFixture struct {
	service *knowledgeNetworkService
	vega    *nullRejectingVega
	objects *nullRejectingObjects
	edge    *interfaces.TypeEdge
}

// newNullKeyFixture builds the issue's relation source_to_target:
// source.source_ref = link.source_ref and link.target_ref = target.ref.
func newNullKeyFixture(links []map[string]any, sources, targets []map[string]any) *nullKeyFixture {
	vega := &nullRejectingVega{backingTableVega{rows: links}}
	objects := &nullRejectingObjects{rows: map[string][]map[string]any{"source": sources, "target": targets}}
	return &nullKeyFixture{
		service: &knowledgeNetworkService{appSetting: &common.AppSetting{}, proxy: &knowledgeNetworkProxyResolverStub{},
			vba: vega, ots: objects},
		vega: vega, objects: objects,
		edge: &interfaces.TypeEdge{
			RelationTypeId: "source_to_target",
			Direction:      interfaces.DIRECTION_FORWARD,
			RelationType: interfaces.RelationType{
				RTID: "source_to_target", RTName: "source_to_target", Type: "indirect",
				SourceObjectTypeID: "source", TargetObjectTypeID: "target",
				MappingRules: &interfaces.InDirectMapping{
					BackingDataSource:  resourceBacking("links"),
					SourceMappingRules: []interfaces.Mapping{nullKeyRule("source_ref", "source_ref")},
					TargetMappingRules: []interfaces.Mapping{nullKeyRule("target_ref", "ref")},
				},
			},
		},
	}
}

func (f *nullKeyFixture) expand(t *testing.T, from []interfaces.LevelObject, nextObjectTypeID string) map[string][]string {
	t.Helper()
	result, err := f.service.getNextObjectsBatchByRelation(context.Background(), pathQuery(), from, f.edge,
		interfaces.ObjectTypeWithKeyField{OTID: nextObjectTypeID, PageQuery: interfaces.PageQuery{Limit: 10}})
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	return reachedInstances(result)
}

var (
	nullKeySource1 = nullKeyObject("source", "1", map[string]any{"source_ref": "S1"})
	nullKeySource2 = nullKeyObject("source", "2", map[string]any{"source_ref": "S2"})
	nullKeyTarget1 = nullKeyObject("target", "T1", map[string]any{"ref": "T1"})
)

// The issue's case: the only backing row of source 1 has a null far key.
func TestIndirectRelationSkipsBackingRowsWithANullFarKey(t *testing.T) {
	for name, link := range map[string]map[string]any{
		"null":    {"source_ref": "S1", "target_ref": nil},
		"missing": {"source_ref": "S1"},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newNullKeyFixture([]map[string]any{link}, nil, []map[string]any{nullKeyTarget1})
			if reached := fixture.expand(t, nullKeyLevelObjects(nullKeySource1), "target"); len(reached) != 0 {
				t.Fatalf("reached %v, want no edges", reached)
			}
			// With no joinable row there is nothing to look up.
			if len(fixture.objects.queries) != 0 {
				t.Fatalf("queried target objects %d times", len(fixture.objects.queries))
			}
		})
	}
}

// A null row in the batch must not fail or hide the valid rows of others.
func TestIndirectRelationKeepsValidRowsBesideANullFarKey(t *testing.T) {
	fixture := newNullKeyFixture([]map[string]any{
		{"source_ref": "S1", "target_ref": nil},
		{"source_ref": "S2", "target_ref": "T1"},
	}, nil, []map[string]any{nullKeyTarget1})
	reached := fixture.expand(t, nullKeyLevelObjects(nullKeySource1, nullKeySource2), "target")
	if fmt.Sprint(reached) != "map[source-2:[target-T1]]" {
		t.Fatalf("reached %v, want only source-2 -> target-T1", reached)
	}
}

// A composite far key with any null or missing component skips the whole row.
func TestIndirectRelationSkipsBackingRowsWithAnIncompleteCompositeFarKey(t *testing.T) {
	fixture := newNullKeyFixture([]map[string]any{
		{"source_ref": "S1", "target_ref": "T1", "target_org": nil},
		{"source_ref": "S1", "target_ref": "T1"},
		{"source_ref": "S2", "target_ref": "T1", "target_org": "X"},
	}, nil, []map[string]any{
		nullKeyObject("target", "T1X", map[string]any{"ref": "T1", "org": "X"}),
		nullKeyObject("target", "T1null", map[string]any{"ref": "T1", "org": nil}),
	})
	rules := fixture.edge.RelationType.MappingRules.(*interfaces.InDirectMapping)
	rules.TargetMappingRules = []interfaces.Mapping{nullKeyRule("target_ref", "ref"), nullKeyRule("target_org", "org")}
	reached := fixture.expand(t, nullKeyLevelObjects(nullKeySource1, nullKeySource2), "target")
	if fmt.Sprint(reached) != "map[source-2:[target-T1X]]" {
		t.Fatalf("reached %v, want only source-2 -> target-T1X", reached)
	}
}

// A current object whose near key is null or missing reads no backing rows,
// and the backing read never filters on a null.
func TestIndirectRelationSkipsCurrentObjectsWithANullNearKey(t *testing.T) {
	links := []map[string]any{
		{"source_ref": nil, "target_ref": "T1"},
		{"source_ref": "S2", "target_ref": "T1"},
	}
	nullSource := nullKeyObject("source", "null", map[string]any{"source_ref": nil})
	missingSource := nullKeyObject("source", "missing", nil)

	fixture := newNullKeyFixture(links, nil, []map[string]any{nullKeyTarget1})
	reached := fixture.expand(t, nullKeyLevelObjects(nullSource, nullKeySource2, missingSource), "target")
	if fmt.Sprint(reached) != "map[source-2:[target-T1]]" {
		t.Fatalf("reached %v, want only source-2 -> target-T1", reached)
	}

	// A batch of only such objects sends no backing read at all: an empty
	// filter would read the whole backing resource.
	fixture = newNullKeyFixture(links, nil, []map[string]any{nullKeyTarget1})
	if reached := fixture.expand(t, nullKeyLevelObjects(nullSource, missingSource), "target"); len(reached) != 0 {
		t.Fatalf("reached %v, want no edges", reached)
	}
	if len(fixture.vega.history) != 0 {
		t.Fatalf("sent %d backing reads for objects without a key", len(fixture.vega.history))
	}
}

// Walking backward swaps the sides: the target's ref is the near key and the
// link's source_ref the far key.
func TestIndirectRelationSkipsNullKeysInReverse(t *testing.T) {
	fixture := newNullKeyFixture([]map[string]any{
		{"source_ref": "S1", "target_ref": "T1"},
		{"source_ref": nil, "target_ref": "T1"},
		{"source_ref": "S2", "target_ref": nil},
	}, []map[string]any{nullKeySource1, nullKeySource2}, nil)
	fixture.edge.Direction = interfaces.DIRECTION_BACKWARD
	nullTarget := nullKeyObject("target", "null", map[string]any{"ref": nil})
	reached := fixture.expand(t, nullKeyLevelObjects(nullKeyTarget1, nullTarget), "source")
	if fmt.Sprint(reached) != "map[target-T1:[source-1]]" {
		t.Fatalf("reached %v, want only target-T1 -> source-1", reached)
	}
}

// A direct relation with a composite key skips objects with a null or
// missing component instead of filtering the next objects on a null.
func TestDirectRelationSkipsObjectsWithAnIncompleteCompositeKey(t *testing.T) {
	fixture := newNullKeyFixture(nil, nil, []map[string]any{
		nullKeyObject("target", "A", map[string]any{"order_no": "A", "org": nil}),
		nullKeyObject("target", "B", map[string]any{"order_no": "B", "org": "X"}),
	})
	fixture.edge.RelationType.Type = "direct"
	fixture.edge.RelationType.MappingRules = []interfaces.Mapping{
		nullKeyRule("order_no", "order_no"), nullKeyRule("org", "org"),
	}
	reached := fixture.expand(t, nullKeyLevelObjects(
		nullKeyObject("source", "A", map[string]any{"order_no": "A", "org": nil}),
		nullKeyObject("source", "A-missing", map[string]any{"order_no": "A"}),
		nullKeyObject("source", "B", map[string]any{"order_no": "B", "org": "X"}),
	), "target")
	if fmt.Sprint(reached) != "map[source-B:[target-B]]" {
		t.Fatalf("reached %v, want only source-B -> target-B", reached)
	}
}

// Matching known objects compares their values in memory: a null on one side
// does not equal a null on the other.
func TestMatchingKnownObjectsNeverJoinsNullToNull(t *testing.T) {
	sources := nullKeyLevelObjects(
		nullKeyObject("source", "null", map[string]any{"ref": nil, "org": nil}),
		nullKeyObject("source", "B", map[string]any{"ref": "B", "org": "X"}),
	)
	targets := nullKeyLevelObjects(
		nullKeyObject("target", "null", map[string]any{"ref": nil, "org": nil}),
		nullKeyObject("target", "B", map[string]any{"ref": "B", "org": "X"}),
	)
	query := &interfaces.SubGraphQueryBaseOnObjects{KNID: "demo", Branch: interfaces.MAIN_BRANCH}
	pairs := func(relations []interfaces.Relation) string {
		names := make([]string, len(relations))
		for index, relation := range relations {
			names[index] = relation.SourceObjectId + "->" + relation.TargetObjectId
		}
		return fmt.Sprint(names)
	}

	for name, rules := range map[string][]interfaces.Mapping{
		"single":    {nullKeyRule("ref", "ref")},
		"composite": {nullKeyRule("ref", "ref"), nullKeyRule("org", "org")},
	} {
		t.Run("direct/"+name, func(t *testing.T) {
			fixture := newNullKeyFixture(nil, nil, nil)
			fixture.edge.RelationType.MappingRules = rules
			relations, err := fixture.service.matchDirectRelations(sources, targets, fixture.edge)
			if err != nil || pairs(relations) != "[source-B->target-B]" {
				t.Fatalf("relations = %s, %v", pairs(relations), err)
			}
		})
	}

	// The backing row of source B carries a null target org, and so does
	// target null: the row reaches no target.
	t.Run("indirect/composite", func(t *testing.T) {
		fixture := newNullKeyFixture([]map[string]any{
			{"source_ref": "B", "target_ref": "B", "target_org": "X"},
			{"source_ref": "B", "target_ref": nil, "target_org": nil},
		}, nil, nil)
		rules := fixture.edge.RelationType.MappingRules.(*interfaces.InDirectMapping)
		rules.SourceMappingRules = []interfaces.Mapping{nullKeyRule("ref", "source_ref")}
		rules.TargetMappingRules = []interfaces.Mapping{nullKeyRule("target_ref", "ref"), nullKeyRule("target_org", "org")}
		relations, err := fixture.service.matchIndirectRelations(context.Background(), query, sources, targets, fixture.edge)
		if err != nil || pairs(relations) != "[source-B->target-B]" {
			t.Fatalf("relations = %s, %v", pairs(relations), err)
		}
	})
}
