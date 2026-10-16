// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

const inferenceQuery = `MATCH (p:Person)-[e:WORKS_AT]->(c:Company)
      -[r:HAS_WORK_LOCATION]->(l:Place)
WHERE p.id = $personId
RETURN p AS source, 'WORKS_IN' AS relationType, l AS target,
       collect({company: c, employment: e, location: r}) AS evidence`

func graphService(t *testing.T) (*cypherQueryService, *recordingVega, *stubPermission) {
	t.Helper()
	person := objectType("person", "Person", resource("people", "people"), dataProperty("id", "person_id"), dataProperty("name", "person_name"))
	person.PrimaryKeys = []string{"id"}
	company := objectType("company", "Company", resource("companies", "companies"), dataProperty("id", "company_id"), dataProperty("person_id", "employee_id"), dataProperty("place_id", "location_id"))
	company.PrimaryKeys = []string{"id"}
	place := objectType("place", "Place", resource("places", "places"), dataProperty("id", "place_id"))
	place.PrimaryKeys = []string{"id"}
	works := relationType("works_at", "WORKS_AT")
	works.SourceObjectTypeID = "person"
	works.TargetObjectTypeID = "company"
	works.MappingRules = []interfaces.Mapping{{SourceProp: interfaces.SimpleProperty{Name: "id"}, TargetProp: interfaces.SimpleProperty{Name: "person_id"}}}
	location := relationType("has_location", "HAS_WORK_LOCATION")
	location.SourceObjectTypeID = "company"
	location.TargetObjectTypeID = "place"
	location.MappingRules = []interfaces.Mapping{{SourceProp: interfaces.SimpleProperty{Name: "place_id"}, TargetProp: interfaces.SimpleProperty{Name: "id"}}}
	vega := &recordingVega{response: &interfaces.RawQueryResponse{Entries: []map[string]any{}}}
	permission := &stubPermission{}
	service := &cypherQueryService{ps: permission, vba: vega, schema: &fakeSchemaSource{objectTypes: []*interfaces.ObjectType{person, company, place}, relationTypes: []*interfaces.RelationType{works, location}}}
	return service, vega, permission
}
func graphRequest(query string) interfaces.CypherQuery {
	return interfaces.CypherQuery{KNID: "kn_1", Branch: "main", Query: query, ResultFormat: interfaces.CYPHER_GRAPH_V1, Parameters: map[string]any{"personId": "p1"}}
}
func graphRow(t *testing.T, compiled *compiledQuery, values map[string]any) map[string]any {
	t.Helper()
	row := map[string]any{}
	for _, column := range compiled.plan.Select {
		ref := compiled.plan.Tables[column.Table].Variable + "." + column.Property
		value, ok := values[ref]
		if !ok && ref == "c.person_id" {
			value, ok = values["p.id"]
		}
		if !ok && ref == "c.place_id" {
			value, ok = values["l.id"]
		}
		if !ok {
			t.Fatalf("missing fixture %s", ref)
		}
		row[column.Alias] = value
	}
	return row
}

func TestGraphOriginalQueryReturnsTypedEvidence(t *testing.T) {
	service, vega, _ := graphService(t)
	request := graphRequest(inferenceQuery)
	request.ObjectProperties = map[string][]string{"p": {"name"}}
	compiled, err := service.compile(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range [][2]string{{"c1", "l1"}, {"c2", "l1"}, {"c2", "l1"}, {"c3", "l2"}} {
		vega.response.Entries = append(vega.response.Entries, graphRow(t, compiled, map[string]any{"p.id": "p1", "p.name": "Alice", "c.id": path[0], "l.id": path[1]}))
	}
	result, err := service.Query(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Columns) != 4 || len(result.Entries) != 2 {
		t.Fatalf("result: %+v", result)
	}
	first := result.Entries[0]
	source := first["source"].(interfaces.CypherObjectValue)
	if source.Key["id"].Value != "p1" || source.Properties["name"] != "Alice" || first["relationType"] != "WORKS_IN" {
		t.Fatalf("source: %+v", first)
	}
	evidence := first["evidence"].([]any)
	if len(evidence) != 3 {
		t.Fatalf("collect lost duplicates: %+v", evidence)
	}
	employment := evidence[0].(map[string]any)["employment"].(interfaces.CypherRelationValue)
	if employment.Source.ObjectTypeID != "person" || employment.Target.ObjectTypeID != "company" || employment.RelationTypeID != "works_at" || !strings.HasPrefix(employment.BindingDigest, "sha256:") {
		t.Fatalf("relation: %+v", employment)
	}
	if result.ValueTypes["evidence"].Element.Fields["employment"].Kind != "relation" || !result.Execution.MatchComplete || result.Execution.MatchRows != 4 {
		t.Fatalf("metadata: %+v", result)
	}
	if vega.request.Paging.Limit != 10000 || !strings.HasSuffix(vega.request.Query, "LIMIT 10000") || strings.Contains(vega.request.Query, "GROUP BY") {
		t.Fatalf("physical plan: %+v", vega.request)
	}
	descriptor := string(result.TraceDescriptor)
	if strings.Contains(descriptor, "_g0") || strings.Contains(descriptor, "person_name") || !strings.Contains(descriptor, `"aggregate":"collect"`) || !strings.Contains(descriptor, `"graph_match_budget":9999`) {
		t.Fatalf("descriptor: %s", descriptor)
	}
}

func TestGraphAggregatesBeforeConclusionLimit(t *testing.T) {
	service, vega, _ := graphService(t)
	request := graphRequest(inferenceQuery + " LIMIT 1")
	compiled, err := service.compile(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1001; i++ {
		vega.response.Entries = append(vega.response.Entries, graphRow(t, compiled, map[string]any{"p.id": "p1", "c.id": fmt.Sprint(i), "l.id": "l1"}))
	}
	vega.response.Entries = append(vega.response.Entries, graphRow(t, compiled, map[string]any{"p.id": "p1", "c.id": "other", "l.id": "l2"}))
	result, err := service.Query(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 1 || len(result.Entries[0]["evidence"].([]any)) != 1001 || !result.Execution.ConclusionsLimited || !result.Execution.EvidenceComplete {
		t.Fatalf("unexpected evidence completeness: %+v", result.Execution)
	}
}

func TestGraphBudgetsRejectIncompleteObservations(t *testing.T) {
	for _, count := range []int{9999, 10000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			service, vega, _ := graphService(t)
			request := graphRequest("MATCH (p:Person) RETURN collect(p.id) AS ids")
			compiled, err := service.compile(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < count; i++ {
				vega.response.Entries = append(vega.response.Entries, graphRow(t, compiled, map[string]any{"p.id": "p1"}))
			}
			result, err := service.Query(context.Background(), request)
			if count == 10000 {
				if err == nil || result != nil || statusOf(t, err) != http.StatusBadRequest {
					t.Fatalf("budget accepted: %+v %v", result, err)
				}
			} else if err != nil || len(result.Entries[0]["ids"].([]any)) != count {
				t.Fatalf("complete input refused: %v", err)
			}
		})
	}
	service, vega, _ := graphService(t)
	request := graphRequest("MATCH (p:Person) RETURN collect(p.id) AS ids")
	compiled, err := service.compile(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	vega.response.Entries = []map[string]any{graphRow(t, compiled, map[string]any{"p.id": strings.Repeat("x", graphPayloadBudget)})}
	if result, err := service.Query(context.Background(), request); err == nil || result != nil {
		t.Fatal("oversized evidence was returned")
	}
}

func TestGraphIdentityAndNullContract(t *testing.T) {
	service, vega, _ := graphService(t)
	source := service.schema.(*fakeSchemaSource)
	person := source.objectTypes[0]
	person.PrimaryKeys = []string{"name", "id"}
	person.DataProperties[0].Type = "integer"
	request := graphRequest("MATCH (p:Person) RETURN p AS person, collect(p.id) AS ids")
	compiled, err := service.compile(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"9007199254740992", "9007199254740993"} {
		vega.response.Entries = append(vega.response.Entries, graphRow(t, compiled, map[string]any{"p.id": json.Number(id), "p.name": "same"}))
	}
	result, err := service.Query(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 2 || len(result.Entries[0]["person"].(interfaces.CypherObjectValue).Key) != 2 {
		t.Fatalf("identity collision: %+v", result.Entries)
	}
	vega.response.Entries[0] = graphRow(t, compiled, map[string]any{"p.id": nil, "p.name": "same"})
	if _, err := service.Query(context.Background(), request); err == nil {
		t.Fatal("null identity accepted")
	}
	for _, typ := range []string{"integer", "unsigned integer"} {
		if _, err := graphKeyValue(typ, json.Number("18446744073709551616")); err == nil {
			t.Fatal("overflow identity accepted")
		}
	}
	if _, err := graphKeyValue("integer", float64(9007199254740992)); err == nil {
		t.Fatal("unsafe float accepted")
	}
}

func TestGraphEmptyAndNullCollect(t *testing.T) {
	for _, query := range []string{inferenceQuery, "MATCH (p:Person) RETURN collect(p.id) AS ids"} {
		service, vega, _ := graphService(t)
		request := graphRequest(query)
		result, err := service.Query(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if query == inferenceQuery {
			if len(result.Entries) != 0 {
				t.Fatal("empty match became a conclusion")
			}
			continue
		}
		if len(result.Entries) != 1 || len(result.Entries[0]["ids"].([]any)) != 0 {
			t.Fatal("empty collect is not []")
		}
		compiled, err := service.compile(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		vega.response.Entries = []map[string]any{graphRow(t, compiled, map[string]any{"p.id": nil})}
		result, err = service.Query(context.Background(), request)
		if err != nil || len(result.Entries[0]["ids"].([]any)) != 0 {
			t.Fatalf("collect(null): %v", err)
		}
	}
}

func TestGraphAuthorizationCoversNestedIdentityAndProperties(t *testing.T) {
	for _, target := range []string{"person.id", "company.id", "person.name", "company.person_id", "company.place_id"} {
		for _, level := range []string{interfaces.PROPERTY_ACCESS_NONE, interfaces.PROPERTY_ACCESS_MASKED} {
			t.Run(target+"/"+level, func(t *testing.T) {
				service, vega, permission := graphService(t)
				parts := strings.Split(target, ".")
				permission.levels = map[string]string{interfaces.KNChildResourceID("kn_1", parts[0]) + "." + parts[1]: level}
				request := graphRequest(inferenceQuery)
				request.ObjectProperties = map[string][]string{"p": {"name"}}
				if _, err := service.Query(context.Background(), request); err == nil {
					t.Fatal("restricted graph field returned")
				}
				if vega.request != nil {
					t.Fatal("unauthorized query executed")
				}
			})
		}
	}
}

func TestGraphRejectsUnsupportedCombinations(t *testing.T) {
	queries := []string{
		"MATCH (p:Person)-[p:WORKS_AT]->(c:Company) RETURN p",
		"MATCH (p:Person)-[e:WORKS_AT]->(c:Company)-[e:HAS_WORK_LOCATION]->(l:Place) RETURN p",
		"MATCH (p:Person) RETURN missing",
		"MATCH (p:Person) RETURN {a:p.id,a:p.id}",
		"MATCH (p:Person) RETURN collect(collect(p.id))",
		"MATCH (p:Person) RETURN p AS x, p AS x",
	}
	for _, query := range queries {
		t.Run(query, func(t *testing.T) {
			service, vega, _ := graphService(t)
			if _, err := service.Query(context.Background(), graphRequest(query)); err == nil {
				t.Fatal("unsupported query succeeded")
			}
			if vega.request != nil {
				t.Fatal("unsupported query executed")
			}
		})
	}
	service, vega, _ := graphService(t)
	request := graphRequest(inferenceQuery)
	request.ResultFormat = ""
	if result, err := service.Query(context.Background(), request); err != nil || vega.request == nil || result.ResultFormat != "graph_v1" {
		t.Fatalf("standard graph syntax requires no format negotiation: %v", err)
	}
}

func TestGraphIncomingRelationAndRowFilters(t *testing.T) {
	service, vega, permission := graphService(t)
	permission.rowFilters = map[string]interfaces.RowFilterPredicate{interfaces.KNChildResourceID("kn_1", "company"): {Kind: "false"}}
	request := graphRequest("MATCH (c:Company)<-[e:WORKS_AT]-(p:Person) RETURN e")
	compiled, err := service.compile(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(compiled.statement, "1 = 0") {
		t.Fatalf("row filter missing: %s", compiled.statement)
	}
	vega.response.Entries = []map[string]any{graphRow(t, compiled, map[string]any{"p.id": "p1", "c.id": "c1"})}
	result, err := service.Query(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	edge := result.Entries[0]["e"].(interfaces.CypherRelationValue)
	if edge.Source.ObjectTypeID != "person" || edge.Target.ObjectTypeID != "company" || len(edge.Basis) != 1 || edge.Basis[0].TargetProperty != "person_id" || edge.Basis[0].TargetValue != "p1" {
		t.Fatalf("wrong relation basis: %+v", edge)
	}
}

func TestGraphRefusesPartialPageAndMissingBindings(t *testing.T) {
	for _, test := range []string{"total", "cursor", "missing_key", "missing_type", "missing_column", "unknown_format", "unknown_properties"} {
		t.Run(test, func(t *testing.T) {
			service, vega, _ := graphService(t)
			request := graphRequest(inferenceQuery)
			compiled, err := service.compile(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			vega.response.Entries = []map[string]any{graphRow(t, compiled, map[string]any{"p.id": "p1", "c.id": "c1", "l.id": "l1"})}
			switch test {
			case "total":
				vega.response.TotalCount = 2
			case "cursor":
				cursor := "remaining"
				vega.response.Paging = &interfaces.ResourceDataPagingResult{NextCursor: &cursor}
			case "missing_key":
				service.schema.(*fakeSchemaSource).objectTypes[0].PrimaryKeys = nil
			case "missing_type":
				service.schema.(*fakeSchemaSource).objectTypes[0].DataProperties[0].Type = "float"
			case "missing_column":
				for key := range vega.response.Entries[0] {
					delete(vega.response.Entries[0], key)
					break
				}
			case "unknown_format":
				request.ResultFormat = "graph_v999"
			case "unknown_properties":
				request.ObjectProperties = map[string][]string{"absent": {"name"}}
			}
			if result, err := service.Query(context.Background(), request); err == nil || result != nil {
				t.Fatal("incomplete or invalid observation returned")
			}
		})
	}
}

func TestGraphRejectsConflictingSnapshotsAndCancellation(t *testing.T) {
	service, vega, _ := graphService(t)
	request := graphRequest(inferenceQuery)
	request.ObjectProperties = map[string][]string{"p": {"name"}}
	compiled, err := service.compile(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"before", "after"} {
		vega.response.Entries = append(vega.response.Entries, graphRow(t, compiled, map[string]any{"p.id": "p1", "p.name": name, "c.id": "c1", "l.id": "l1"}))
	}
	if result, err := service.Query(context.Background(), request); err == nil || result != nil {
		t.Fatal("contradictory snapshot was silently merged")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result, err := assembleGraph(ctx, compiled.plan, vega.response.Entries[:1]); err == nil || result != nil {
		t.Fatal("cancelled observation was assembled")
	}
}

func TestGraphHiddenImplicitFieldsAreNotDisclosed(t *testing.T) {
	for _, field := range []string{"primary", "basis"} {
		t.Run(field, func(t *testing.T) {
			service, vega, permission := graphService(t)
			source := service.schema.(*fakeSchemaSource)
			company := source.objectTypes[1]
			if field == "primary" {
				company.DataProperties[0].Name = "undisclosed_key"
				company.PrimaryKeys = []string{"undisclosed_key"}
			} else {
				company.DataProperties[1].Name = "undisclosed_key"
				source.relationTypes[0].MappingRules.([]interfaces.Mapping)[0].TargetProp.Name = "undisclosed_key"
			}
			permission.levels = map[string]string{interfaces.KNChildResourceID("kn_1", "company") + ".undisclosed_key": interfaces.PROPERTY_ACCESS_NONE}
			_, err := service.Query(context.Background(), graphRequest(inferenceQuery))
			if err == nil || strings.Contains(err.Error(), "undisclosed_key") || vega.request != nil {
				t.Fatalf("hidden implicit field exposed: %v", err)
			}
		})
	}
}

func TestGraphDescriptorKeepsLogicalPropertiesSharingAColumn(t *testing.T) {
	service, _, _ := graphService(t)
	person := service.schema.(*fakeSchemaSource).objectTypes[0]
	person.DataProperties[1].MappedField.Name = person.DataProperties[0].MappedField.Name
	compiled, err := service.compile(context.Background(), graphRequest("MATCH (p:Person) RETURN p.id AS id, p.name AS name"))
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled.plan.Select) != 1 || !strings.Contains(string(compiled.traceDescriptor), "property:kn_1:person:name") {
		t.Fatalf("logical meaning lost: %s", compiled.traceDescriptor)
	}
}
