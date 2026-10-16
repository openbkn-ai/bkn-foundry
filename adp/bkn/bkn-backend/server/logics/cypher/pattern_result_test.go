// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

type patternFixtureData struct {
	nodes map[string][]map[string]any
	edges map[string][][2]string
}

func patternTestService(t *testing.T) (*cypherQueryService, patternFixtureData) {
	service, _, _ := graphService(t)
	schema := service.schema.(*fakeSchemaSource)
	person := schema.objectTypes[0]
	person.DataProperties = append(person.DataProperties, dataProperty("next", "next_id"))
	friend := relationType("friend", "FRIEND")
	friend.SourceObjectTypeID = "person"
	friend.TargetObjectTypeID = "person"
	friend.MappingRules = []interfaces.Mapping{{SourceProp: interfaces.SimpleProperty{Name: "next"}, TargetProp: interfaces.SimpleProperty{Name: "id"}}}
	self := relationType("self", "SELF")
	self.SourceObjectTypeID = "person"
	self.TargetObjectTypeID = "person"
	self.MappingRules = []interfaces.Mapping{{SourceProp: interfaces.SimpleProperty{Name: "id"}, TargetProp: interfaces.SimpleProperty{Name: "id"}}}
	schema.relationTypes = append(schema.relationTypes, friend, self)
	return service, patternFixtureData{nodes: map[string][]map[string]any{
		"person":  {{"id": "p1", "name": "Alice", "next": "p2"}, {"id": "p2", "name": "Bob", "next": "p3"}, {"id": "p3", "name": "Cara", "next": "p1"}},
		"company": {{"id": "c1", "person_id": "p1", "place_id": "l1"}}, "place": {{"id": "l1"}},
	}, edges: map[string][][2]string{"friend": {{"p1", "p2"}, {"p2", "p3"}, {"p3", "p1"}}, "self": {{"p1", "p1"}, {"p2", "p2"}, {"p3", "p3"}}, "works_at": {{"p1", "c1"}}, "has_location": {{"c1", "l1"}}}}
}
func attachPatternFixture(t *testing.T, service *cypherQueryService, request interfaces.CypherQuery, data patternFixtureData) *queuedUnionVega {
	t.Helper()
	compiled, err := service.compile(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	vega := &queuedUnionVega{responsesByStatement: map[string]*interfaces.RawQueryResponse{}}
	find := func(typeID, id string) map[string]any {
		for _, node := range data.nodes[typeID] {
			if node["id"] == id {
				return node
			}
		}
		t.Fatalf("missing fixture node %s:%s", typeID, id)
		return nil
	}
	var add func(*compiledQuery)
	add = func(plan *compiledQuery) {
		for _, child := range plan.stages {
			add(child)
		}
		for _, child := range plan.branches {
			add(child)
		}
		for _, child := range plan.observations {
			add(child)
		}
		for _, child := range plan.subqueries {
			add(child)
		}
		if len(plan.plan.Tables) == 0 {
			return
		}
		response := &interfaces.RawQueryResponse{Entries: []map[string]any{}}
		physical := plan.plan
		sources := [][]map[string]any{}
		if len(physical.Tables) == 1 {
			for _, node := range data.nodes[physical.Tables[0].ObjectTypeID] {
				sources = append(sources, []map[string]any{node})
			}
		} else if len(physical.Tables) == 2 && len(physical.Joins) == 1 {
			for _, edge := range data.edges[physical.Joins[0].RelationTypeID] {
				sources = append(sources, []map[string]any{find(physical.Tables[0].ObjectTypeID, edge[0]), find(physical.Tables[1].ObjectTypeID, edge[1])})
			}
		} else {
			t.Fatalf("unhandled physical fixture %+v", physical.Tables)
		}
		for _, source := range sources {
			row := map[string]any{}
			for _, column := range physical.Select {
				value, ok := source[column.Table][column.Property]
				if !ok {
					t.Fatalf("missing physical fixture %s.%s", physical.Tables[column.Table].ObjectTypeID, column.Property)
				}
				row[column.Alias] = value
			}
			response.Entries = append(response.Entries, row)
		}
		vega.responses = append(vega.responses, response)
		vega.responsesByStatement[plan.statement] = response
	}
	add(compiled)
	service.vba = vega
	return vega
}

func TestGeneralPatternSemantics(t *testing.T) {
	tests := []struct {
		query string
		rows  []map[string]any
	}{
		{"MATCH route=(a:Person)-[:FRIEND*1..2]->(b:Person) WHERE a.id='p1' RETURN b.id AS id,length(route) AS n ORDER BY n", []map[string]any{{"id": "p2", "n": int64(1)}, {"id": "p3", "n": int64(2)}}},
		{"MATCH route=(a:Person)-[:FRIEND*0..1]->(b:Person) WHERE a.id='p1' RETURN b.id AS id,length(route) AS n ORDER BY n", []map[string]any{{"id": "p1", "n": int64(0)}, {"id": "p2", "n": int64(1)}}},
		{"MATCH route=(a:Person)-[:FRIEND*0]->(b:Person) WHERE a.id='p1' RETURN b.id AS id,length(route) AS n", []map[string]any{{"id": "p1", "n": int64(0)}}},
		{"MATCH route=(a:Person)-[:FRIEND*]->(b:Person) WHERE a.id='p1' RETURN b.id AS id,length(route) AS n ORDER BY n", []map[string]any{{"id": "p2", "n": int64(1)}, {"id": "p3", "n": int64(2)}, {"id": "p1", "n": int64(3)}}},
		{"MATCH (a:Person)-[:FRIEND*4..5]->(b:Person) RETURN b.id AS id", []map[string]any{}},
		{"MATCH (a:Person)-[r:FRIEND*1..2]->(b:Person) WHERE a.id='p1' RETURN size(r) AS n,type(head(r)) AS type ORDER BY n", []map[string]any{{"n": int64(1), "type": "friend"}, {"n": int64(2), "type": "friend"}}},
		{"MATCH (a:Person)-[r:FRIEND]-(b:Person) WHERE a.id='p1' RETURN b.id AS id,type(r) AS type ORDER BY id", []map[string]any{{"id": "p2", "type": "friend"}, {"id": "p3", "type": "friend"}}},
		{"MATCH (a:Person)-[r:SELF]-(b:Person) WHERE a.id='p1' RETURN b.id AS id,type(r) AS type", []map[string]any{{"id": "p1", "type": "self"}}},
		{"MATCH (a:Person)-[r:FRIEND|WORKS_AT]->(b) WHERE a.id='p1' RETURN b.id AS id,type(r) AS type ORDER BY id", []map[string]any{{"id": "c1", "type": "works_at"}, {"id": "p2", "type": "friend"}}},
		{"MATCH (a:Person)-[r]->(b) WHERE a.id='p1' RETURN b.id AS id,type(r) AS type ORDER BY id", []map[string]any{{"id": "c1", "type": "works_at"}, {"id": "p1", "type": "self"}, {"id": "p2", "type": "friend"}}},
		{"MATCH (a:Person)-[r:FRIEND]->(b:Person) RETURN DISTINCT labels(a) AS labels,properties(r) AS properties,r.weight AS weight", []map[string]any{{"labels": []any{"person"}, "properties": map[string]any{}, "weight": nil}}},
		{"MATCH (n) RETURN n.id AS id ORDER BY id", []map[string]any{{"id": "c1"}, {"id": "l1"}, {"id": "p1"}, {"id": "p2"}, {"id": "p3"}}},
		{"MATCH (n) WHERE n.id='c1' RETURN n.name AS name", []map[string]any{{"name": nil}}},
		{"UNWIND ['p1'] AS id MATCH route=(a:Person)-[:FRIEND*1..2]->(b) WHERE a.id=id RETURN b.id AS id,length(route) AS n ORDER BY n", []map[string]any{{"id": "p2", "n": int64(1)}, {"id": "p3", "n": int64(2)}}},
		{"OPTIONAL MATCH route=(a:Person)-[:FRIEND*4..5]->(b) RETURN route,b.id AS id", []map[string]any{{"route": nil, "id": nil}}},
		{"MATCH (a:Person)-[:FRIEND*1..2]->(b) WHERE a.id='p1' WITH b AS q MATCH (q)-[:FRIEND]->(c:Person) RETURN c.id AS id ORDER BY id", []map[string]any{{"id": "p1"}, {"id": "p3"}}},
	}
	for _, test := range tests {
		t.Run(test.query, func(t *testing.T) {
			service, data := patternTestService(t)
			request := graphRequest(test.query)
			request.ResultFormat = ""
			attachPatternFixture(t, service, request, data)
			result, err := service.Query(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			assertExpressionRows(t, result.Entries, test.rows)
		})
	}
}

func TestGeneralPatternAuthorizationAndCompleteness(t *testing.T) {
	for _, query := range []string{
		"MATCH (a)-[:FRIEND*1..2]->(b) RETURN b.name AS name",
		"MATCH route=(a:Person)-[:FRIEND*1..2]->(b:Person) RETURN [n IN nodes(route) | n.name] AS names",
	} {
		service, _ := patternTestService(t)
		permission := service.ps.(*stubPermission)
		permission.levels = map[string]string{"kn_1/person.name": "masked"}
		original := service.vba.(*recordingVega)
		if result, err := service.Query(context.Background(), graphRequest(query)); err == nil || result != nil || original.request != nil {
			t.Fatal("runtime pattern bypassed pre-read authorization", query, err)
		}
	}
	service, data := patternTestService(t)
	request := graphRequest("MATCH route=(a:Person)-[:FRIEND*1..2]->(b:Person) RETURN route")
	vega := attachPatternFixture(t, service, request, data)
	vega.responses[0].TotalCount = 10000
	if result, err := service.Query(context.Background(), request); err == nil || result != nil || !strings.Contains(err.Error(), "LimitExceeded") {
		t.Fatal("incomplete graph accepted", err)
	}
}

func TestUnboundedPatternDepthCannotClaimCompleteness(t *testing.T) {
	for _, bounded := range []bool{false, true} {
		service, data := patternTestService(t)
		data.nodes["person"] = nil
		data.edges["friend"] = nil
		for i := 0; i < 10; i++ {
			data.nodes["person"] = append(data.nodes["person"], map[string]any{"id": fmt.Sprintf("p%d", i), "name": "Node", "next": fmt.Sprintf("p%d", i+1)})
			if i < 9 {
				data.edges["friend"] = append(data.edges["friend"], [2]string{fmt.Sprintf("p%d", i), fmt.Sprintf("p%d", i+1)})
			}
		}
		length := "*"
		if bounded {
			length = "*1..8"
		}
		request := graphRequest("MATCH route=(a:Person)-[:FRIEND" + length + "]->(b) WHERE a.id='p0' RETURN length(route) AS hops ORDER BY hops")
		attachPatternFixture(t, service, request, data)
		result, err := service.Query(context.Background(), request)
		if !bounded {
			if err == nil || result != nil || !strings.Contains(err.Error(), "LimitExceeded") {
				t.Fatal("unbounded traversal silently truncated", err)
			}
		} else if err != nil || len(result.Entries) != 8 || !result.Execution.EvidenceComplete {
			t.Fatal("explicit bounded traversal failed", err)
		}
	}
}
