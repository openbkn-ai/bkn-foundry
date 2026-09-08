// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledge_network

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	cond "ontology-query/common/condition"
	"ontology-query/interfaces"
)

type relationPropertyAccessStub struct {
	levels map[string]interfaces.PropertyAccessLevel
}

func (stub relationPropertyAccessStub) ResolvePropertyLevels(_ context.Context,
	items []interfaces.PropertyLevelsRequestItem) ([]interfaces.PropertyLevelsDecisionEntry, error) {
	entries := make([]interfaces.PropertyLevelsDecisionEntry, 0, len(items))
	for _, item := range items {
		entry := interfaces.PropertyLevelsDecisionEntry{ObjectTypeRef: item.ObjectTypeRef}
		for _, name := range item.Properties {
			entry.Properties = append(entry.Properties, interfaces.PropertyAccessDecision{
				Name: name, Level: stub.levels[item.ObjectTypeRef+"/"+name],
			})
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func TestRelationMappingRequiresFullPropertiesOnBothSides(t *testing.T) {
	service := &knowledgeNetworkService{propertyAccess: relationPropertyAccessStub{levels: map[string]interfaces.PropertyAccessLevel{
		"kn-1/source/join_key":    interfaces.PropertyAccessMasked,
		"kn-1/target/foreign_key": interfaces.PropertyAccessFull,
	}}}
	objectTypes := map[string]*interfaces.ObjectType{
		"source": relationAccessObjectType("source", "join_key"),
		"target": relationAccessObjectType("target", "foreign_key"),
	}
	relationTypes := map[string]interfaces.RelationType{"relation": {
		RTID: "relation", SourceObjectTypeID: "source", TargetObjectTypeID: "target",
		MappingRules: []interfaces.Mapping{{
			SourceProp: interfaces.SimpleProperty{Name: "join_key"},
			TargetProp: interfaces.SimpleProperty{Name: "foreign_key"},
		}},
	}}

	err := service.requireFullRelationInputs(context.Background(), "kn-1", objectTypes, relationTypes)
	httpError, ok := err.(*rest.HTTPError)
	if !ok || httpError.HTTPCode != http.StatusBadRequest {
		t.Fatalf("relation property access error = %#v", err)
	}
}

func TestFilteredCrossJoinWildcardRequiresEveryPropertyToBeFull(t *testing.T) {
	service := &knowledgeNetworkService{propertyAccess: relationPropertyAccessStub{levels: map[string]interfaces.PropertyAccessLevel{
		"kn-1/source/id":     interfaces.PropertyAccessFull,
		"kn-1/source/secret": interfaces.PropertyAccessMasked,
		"kn-1/target/id":     interfaces.PropertyAccessFull,
	}}}
	objectTypes := map[string]*interfaces.ObjectType{
		"source": relationAccessObjectType("source", "id", "secret"),
		"target": relationAccessObjectType("target", "id"),
	}
	relationTypes := map[string]interfaces.RelationType{"relation": {
		RTID: "relation", SourceObjectTypeID: "source", TargetObjectTypeID: "target",
		MappingRules: &interfaces.FilteredCrossJoinMapping{
			SourceCondition: &cond.CondCfg{Name: "*", Operation: cond.OperationEq},
		},
	}}

	err := service.requireFullRelationInputs(context.Background(), "kn-1", objectTypes, relationTypes)
	httpError, ok := err.(*rest.HTTPError)
	if !ok || httpError.HTTPCode != http.StatusBadRequest {
		t.Fatalf("filtered cross join property access error = %#v", err)
	}
}

func TestSubgraphProjectionUsesOnlySanitizedSystemFields(t *testing.T) {
	objectType := relationAccessObjectType("customer", "id")
	objectType.DisplayKey = "mobile"
	objects := interfaces.Objects{
		ObjectType: objectType,
		EffectivePermissions: map[string]interfaces.PropertyAccessLevel{
			"id": interfaces.PropertyAccessFull, "mobile": interfaces.PropertyAccessMasked,
		},
	}
	row := map[string]any{
		"id": "customer-1", "mobile": "1*********8",
		interfaces.SYSTEM_PROPERTY_INSTANCE_ID:       "customer-customer-1",
		interfaces.SYSTEM_PROPERTY_INSTANCE_IDENTITY: map[string]any{"id": "customer-1"},
		interfaces.SYSTEM_PROPERTY_DISPLAY:           "1*********8",
		interfaces.SORT_FIELD_SCORE:                  0.9,
	}
	levelObject, ok := levelObjectFromProjectedRow(row, objects)
	if !ok {
		t.Fatal("sanitized identity should be usable by the relation graph")
	}
	info := objectInfoFromLevelObject(levelObject, nil)
	if info.InstanceID != "customer-customer-1" || info.Display != "1*********8" {
		t.Fatalf("system projection = %#v", info.ObjectSystemInfo)
	}
	for _, systemField := range []string{
		interfaces.SYSTEM_PROPERTY_INSTANCE_ID, interfaces.SYSTEM_PROPERTY_INSTANCE_IDENTITY,
		interfaces.SYSTEM_PROPERTY_DISPLAY, interfaces.SORT_FIELD_SCORE,
	} {
		if _, leaked := info.Properties[systemField]; leaked {
			t.Fatalf("system field %q leaked into properties: %#v", systemField, info.Properties)
		}
	}
	excluded := objectInfoFromLevelObject(levelObject, []string{
		interfaces.SYSTEM_PROPERTY_INSTANCE_ID,
		interfaces.SYSTEM_PROPERTY_INSTANCE_IDENTITY,
		interfaces.SYSTEM_PROPERTY_DISPLAY,
	})
	body, err := json.Marshal(excluded)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "_instance_id") || strings.Contains(string(body), "_display") {
		t.Fatalf("excluded system fields were serialized: %s", body)
	}

	partialPrimary := objects
	partialPrimary.EffectivePermissions["id"] = interfaces.PropertyAccessMasked
	if _, ok := levelObjectFromProjectedRow(map[string]any{"id": "raw-primary-key"}, partialPrimary); ok {
		t.Fatal("a raw or masked primary key must not be concatenated into a subgraph object id")
	}
}

func TestSubgraphResponseExposesOpaqueCursorOnly(t *testing.T) {
	body, err := json.Marshal(interfaces.ObjectSubGraph{Cursor: "opaque-cursor"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "search_after") || !strings.Contains(string(body), "opaque-cursor") {
		t.Fatalf("subgraph pagination response = %s", body)
	}
}

func relationAccessObjectType(id string, properties ...string) *interfaces.ObjectType {
	result := &interfaces.ObjectType{KNID: "kn-1", ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: id}}
	for _, property := range properties {
		result.DataProperties = append(result.DataProperties, cond.DataProperty{Name: property, Type: "string"})
	}
	return result
}
