// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// indirectObjectTypes and indirectRelationTypes model the network of #2653:
// employees and projects linked through a membership table, each side on two
// fields that must hold on the same membership row.
func indirectObjectTypes() []*interfaces.ObjectType {
	employee := objectType("ot_employee", "employee", resource("res_employee", "employees"),
		dataProperty("employee_uid", "f_uid"),
		dataProperty("employee_code", "f_code"),
		dataProperty("org_code", "f_org"),
		dataProperty("employee_name", "f_name"),
	)
	employee.PrimaryKeys = []string{"employee_uid"}
	project := objectType("ot_project", "project", resource("res_project", "projects"),
		dataProperty("project_uid", "f_uid"),
		dataProperty("project_code", "f_code"),
		dataProperty("org_code", "f_org"),
		dataProperty("project_name", "f_name"),
	)
	project.PrimaryKeys = []string{"project_uid"}
	return []*interfaces.ObjectType{employee, project}
}

func rule(source, target string) interfaces.Mapping {
	return interfaces.Mapping{
		SourceProp: interfaces.SimpleProperty{Name: source},
		TargetProp: interfaces.SimpleProperty{Name: target},
	}
}

func indirectRelationTypes() []*interfaces.RelationType {
	participates := relationType("rt_participates", "participates_in")
	participates.Type = interfaces.RELATION_TYPE_INDIRECT
	participates.SourceObjectTypeID = "ot_employee"
	participates.TargetObjectTypeID = "ot_project"
	participates.MappingRules = &interfaces.InDirectMapping{
		BackingDataSource: &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "res_member"},
		SourceMappingRules: []interfaces.Mapping{
			rule("employee_code", "employee_code"),
			rule("org_code", "employee_org_code"),
		},
		TargetMappingRules: []interfaces.Mapping{
			rule("project_code", "project_code"),
			rule("project_org_code", "org_code"),
		},
	}

	// An indirect relation from employees to employees, which an undirected
	// pattern cannot settle.
	mentors := relationType("rt_mentors", "mentors")
	mentors.Type = interfaces.RELATION_TYPE_INDIRECT
	mentors.SourceObjectTypeID = "ot_employee"
	mentors.TargetObjectTypeID = "ot_employee"
	mentors.MappingRules = &interfaces.InDirectMapping{
		BackingDataSource:  &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "res_mentoring"},
		SourceMappingRules: []interfaces.Mapping{rule("employee_uid", "mentor_uid")},
		TargetMappingRules: []interfaces.Mapping{rule("mentee_uid", "employee_uid")},
	}

	unbacked := relationType("rt_unbacked", "unbacked")
	unbacked.Type = interfaces.RELATION_TYPE_INDIRECT
	unbacked.SourceObjectTypeID = "ot_employee"
	unbacked.TargetObjectTypeID = "ot_project"
	unbacked.MappingRules = &interfaces.InDirectMapping{
		SourceMappingRules: []interfaces.Mapping{rule("employee_code", "employee_code")},
		TargetMappingRules: []interfaces.Mapping{rule("project_code", "project_code")},
	}
	return []*interfaces.RelationType{participates, mentors, unbacked}
}

func indirectSchema(t *testing.T) *Schema {
	t.Helper()
	return testSchema(t, &fakeSchemaSource{objectTypes: indirectObjectTypes(), relationTypes: indirectRelationTypes()})
}

func compileIndirect(t *testing.T, query string, options CompileOptions) (*Plan, error) {
	t.Helper()
	tree, err := Parse(query)
	if err != nil {
		t.Fatalf("Parse(%q): %v", query, err)
	}
	analyzed, err := Analyze(tree)
	if err != nil {
		return nil, err
	}
	return Compile(analyzed, indirectSchema(t), options)
}

func mustGenerateIndirect(t *testing.T, query string) string {
	t.Helper()
	plan, err := compileIndirect(t, query, CompileOptions{})
	if err != nil {
		t.Fatalf("Compile(%q): %v", query, err)
	}
	if !plan.SQLOnly {
		t.Fatalf("a plan over a backing resource must stay on the SQL path")
	}
	statement, err := Generate(plan, GenerateOptions{})
	if err != nil {
		t.Fatalf("Generate(%q): %v", query, err)
	}
	return statement
}

// The backing row has to match both fields of each side; a row matching one
// field of a side links nothing.
const (
	forwardLinks = "JOIN {{.res_member}} t2 ON t2.`employee_code` = t0.`f_code` AND t2.`employee_org_code` = t0.`f_org` " +
		"JOIN {{.res_project}} t1 ON t2.`project_code` = t1.`f_code` AND t2.`project_org_code` = t1.`f_org`"
)

func TestIndirectRelationJoinsThroughItsBackingRow(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "forward",
			query: "MATCH (a:employee)-[:participates_in]->(b:project) RETURN a.employee_uid, b.project_uid LIMIT 25",
			want: "SELECT t0.`f_uid` AS `a.employee_uid`, t1.`f_uid` AS `b.project_uid` FROM {{.res_employee}} t0 " +
				forwardLinks + " LIMIT 25",
		},
		{
			name:  "written from the target",
			query: "MATCH (b:project)<-[:participates_in]-(a:employee) RETURN a.employee_uid, b.project_uid",
			want: "SELECT t1.`f_uid` AS `a.employee_uid`, t0.`f_uid` AS `b.project_uid` FROM {{.res_project}} t0 " +
				"JOIN {{.res_member}} t2 ON t2.`project_code` = t0.`f_code` AND t2.`project_org_code` = t0.`f_org` " +
				"JOIN {{.res_employee}} t1 ON t2.`employee_code` = t1.`f_code` AND t2.`employee_org_code` = t1.`f_org`",
		},
		{
			name:  "undirected between different object types",
			query: "MATCH (a:employee)-[:participates_in]-(b:project) RETURN a.employee_uid, b.project_uid",
			want: "SELECT t0.`f_uid` AS `a.employee_uid`, t1.`f_uid` AS `b.project_uid` FROM {{.res_employee}} t0 " +
				forwardLinks,
		},
		{
			name:  "count",
			query: "MATCH (a:employee)-[:participates_in]->(b:project) RETURN count(*) AS links",
			want:  "SELECT COUNT(*) AS `links` FROM {{.res_employee}} t0 " + forwardLinks,
		},
		{
			name:  "filtered on both ends",
			query: "MATCH (a:employee)-[:participates_in]->(b:project) WHERE a.org_code = 'ORG1' AND b.project_code = 'P01' RETURN a.employee_uid",
			want: "SELECT t0.`f_uid` AS `a.employee_uid` FROM {{.res_employee}} t0 " + forwardLinks +
				" WHERE t0.`f_org` = 'ORG1' AND t1.`f_code` = 'P01'",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := mustGenerateIndirect(t, tc.query); got != tc.want {
				t.Fatalf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

func TestIndirectRelationTwoHopsStayDistinct(t *testing.T) {
	// Two employees on one project: each hop reads a backing row of its own,
	// and the two hops may not be the same link.
	got := mustGenerateIndirect(t, "MATCH (a:employee)-[:participates_in]->(p:project)<-[:participates_in]-(b:employee) "+
		"RETURN a.employee_uid, b.employee_uid")
	want := "SELECT t0.`f_uid` AS `a.employee_uid`, t2.`f_uid` AS `b.employee_uid` FROM {{.res_employee}} t0 " +
		"JOIN {{.res_member}} t3 ON t3.`employee_code` = t0.`f_code` AND t3.`employee_org_code` = t0.`f_org` " +
		"JOIN {{.res_project}} t1 ON t3.`project_code` = t1.`f_code` AND t3.`project_org_code` = t1.`f_org` " +
		"JOIN {{.res_member}} t4 ON t4.`project_code` = t1.`f_code` AND t4.`project_org_code` = t1.`f_org` " +
		"JOIN {{.res_employee}} t2 ON t4.`employee_code` = t2.`f_code` AND t4.`employee_org_code` = t2.`f_org` " +
		"WHERE NOT t0.`f_uid` = t2.`f_uid`"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestIndirectRelationClosingACycleJoinsOnlyItsBackingTable(t *testing.T) {
	got := mustGenerateIndirect(t, "MATCH (a:employee)-[:mentors]->(a) RETURN a.employee_uid")
	want := "SELECT t0.`f_uid` AS `a.employee_uid` FROM {{.res_employee}} t0 " +
		"JOIN {{.res_mentoring}} t1 ON t1.`mentor_uid` = t0.`f_uid` AND t1.`mentee_uid` = t0.`f_uid`"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestIndirectRelationOptionalMatchNestsItsBackingJoin(t *testing.T) {
	// A membership row whose project is missing, or filtered out by the
	// optional clause, must not come back as a row of its own beside the
	// employee's real projects; the nested join keeps one row per real match
	// and a single row of nulls when there is none.
	got := mustGenerateIndirect(t, "MATCH (a:employee) OPTIONAL MATCH (a)-[:participates_in]->(b:project) "+
		"WHERE b.org_code = 'ORG1' RETURN a.employee_uid, b.project_uid")
	want := "SELECT t0.`f_uid` AS `a.employee_uid`, t1.`f_uid` AS `b.project_uid` FROM {{.res_employee}} t0 " +
		"LEFT JOIN ({{.res_member}} t2 JOIN {{.res_project}} t1 ON t2.`project_code` = t1.`f_code` AND t2.`project_org_code` = t1.`f_org`) " +
		"ON t2.`employee_code` = t0.`f_code` AND t2.`employee_org_code` = t0.`f_org` AND t1.`f_org` = 'ORG1'"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestIndirectRelationRefusals(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "undirected over a relation to the same object type",
			query: "MATCH (a:employee)-[:mentors]-(b:employee) RETURN a.employee_uid",
			want:  "write its direction",
		},
		{
			name:  "no backing resource",
			query: "MATCH (a:employee)-[:unbacked]->(b:project) RETURN a.employee_uid",
			want:  "no backing resource",
		},
		{
			name:  "written backwards",
			query: "MATCH (b:project)-[:participates_in]->(a:employee) RETURN a.employee_uid",
			want:  "does not connect",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := compileIndirect(t, tc.query, CompileOptions{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

// RETURN r gives the relationship with its two ends and, as its basis, every
// mapping rule of the model read from the membership row that linked them.
func TestIndirectRelationshipValue(t *testing.T) {
	plan, err := compileIndirect(t, "MATCH (a:employee)-[r:participates_in]->(b:project) RETURN a, r, b LIMIT 25",
		CompileOptions{ObjectProperties: map[string][]string{"a": {"employee_name"}, "b": {"project_name"}}})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	read, err := planGraphRead(plan, DialectMySQL, interfaces.CYPHER_DEFAULT_LIMIT)
	if err != nil {
		t.Fatalf("planGraphRead: %v", err)
	}
	if !strings.Contains(read.statement, forwardLinks) {
		t.Fatalf("statement %s does not join through the membership row", read.statement)
	}

	// One statement row, keyed by the read's own aliases.
	values := map[[2]any]any{
		{0, "f_uid"}: "E01", {0, "f_name"}: "Ada", {0, "f_code"}: "E01", {0, "f_org"}: "ORG1",
		{1, "f_uid"}: "P03", {1, "f_name"}: "Apollo", {1, "f_code"}: "P03", {1, "f_org"}: "ORG2",
		{2, "employee_code"}: "E01", {2, "employee_org_code"}: "ORG1",
		{2, "project_code"}: "P03", {2, "project_org_code"}: "ORG2",
	}
	entry := map[string]any{}
	for _, column := range read.columns {
		value, ok := values[[2]any{column.table, column.column}]
		if !ok {
			t.Fatalf("the read selects table %d column %q, which the test row does not have", column.table, column.column)
		}
		entry[column.alias] = value
	}
	rows, _, err := assembleGraphRows(plan, read, []map[string]any{entry})
	if err != nil {
		t.Fatalf("assembleGraphRows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	relation, ok := rows[0]["r"].(interfaces.CypherRelationValue)
	if !ok {
		t.Fatalf("r = %#v, want a relation value", rows[0]["r"])
	}
	if relation.RelationTypeID != "rt_participates" || relation.Source.ObjectTypeID != "ot_employee" ||
		relation.Target.ObjectTypeID != "ot_project" {
		t.Fatalf("relation = %+v", relation)
	}
	if got := relation.Source.Key["employee_uid"].Value; got != "E01" {
		t.Fatalf("source key = %v, want E01", got)
	}
	if got := relation.Target.Key["project_uid"].Value; got != "P03" {
		t.Fatalf("target key = %v, want P03", got)
	}
	wantBasis := []interfaces.CypherRelationBasis{
		{SourceProperty: "employee_code", SourceValue: "E01", TargetProperty: "employee_code", TargetValue: "E01"},
		{SourceProperty: "org_code", SourceValue: "ORG1", TargetProperty: "employee_org_code", TargetValue: "ORG1"},
		{SourceProperty: "project_code", SourceValue: "P03", TargetProperty: "project_code", TargetValue: "P03"},
		{SourceProperty: "project_org_code", SourceValue: "ORG2", TargetProperty: "org_code", TargetValue: "ORG2"},
	}
	if !reflect.DeepEqual(relation.Basis, wantBasis) {
		got, _ := json.Marshal(relation.Basis)
		t.Fatalf("basis = %s", got)
	}
	if !strings.HasPrefix(relation.BindingDigest, "sha256:") {
		t.Fatalf("binding digest = %q", relation.BindingDigest)
	}
}

func indirectService(permission *stubPermission, vega *recordingVega) *cypherQueryService {
	return &cypherQueryService{
		ps:     permission,
		schema: &fakeSchemaSource{objectTypes: indirectObjectTypes(), relationTypes: indirectRelationTypes()},
		vba:    vega,
	}
}

// The backing table is a resource, not an object: the row policy is asked for
// the two ends only -- never for an object reference with no object type,
// which the policy service refuses -- and applies to both.
func TestIndirectRelationRowFiltersCoverBothEndsOnly(t *testing.T) {
	org1, p01 := "ORG1", "P01"
	permission := &stubPermission{rowFilters: map[string]interfaces.RowFilterPredicate{
		"kn_1/ot_employee": {Kind: "in", Property: "org_code", Values: []interfaces.RowFilterValue{{Type: "string", String: &org1}}},
		"kn_1/ot_project":  {Kind: "in", Property: "project_code", Values: []interfaces.RowFilterValue{{Type: "string", String: &p01}}},
	}}
	vega := &recordingVega{}
	_, err := indirectService(permission, vega).Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: "main",
		Query: "MATCH (a:employee)-[:participates_in]->(b:project) RETURN count(*) AS links",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(permission.rowFilterCalls) != 1 {
		t.Fatalf("row-filter calls = %d, want 1", len(permission.rowFilterCalls))
	}
	if got, want := permission.rowFilterCalls[0], []string{"kn_1/ot_employee", "kn_1/ot_project"}; !slices.Equal(got, want) {
		t.Fatalf("row-filter refs = %v, want %v", got, want)
	}
	want := "SELECT COUNT(*) AS `links` FROM {{.res_employee}} t0 " + forwardLinks +
		" WHERE t0.`f_org` IN ('ORG1') AND t1.`f_code` IN ('P01') LIMIT 1000"
	if got := vega.request.Query; got != want {
		t.Fatalf("statement = %s\nwant      = %s", got, want)
	}
}

func TestIndirectRelationOptionalRowFilterStaysInTheJoin(t *testing.T) {
	p01 := "P01"
	permission := &stubPermission{rowFilters: map[string]interfaces.RowFilterPredicate{
		"kn_1/ot_project": {Kind: "in", Property: "project_code", Values: []interfaces.RowFilterValue{{Type: "string", String: &p01}}},
	}}
	vega := &recordingVega{}
	_, err := indirectService(permission, vega).Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: "main",
		Query: "MATCH (a:employee) OPTIONAL MATCH (a)-[:participates_in]->(b:project) RETURN a.employee_uid, b.project_uid",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	// A hidden project leaves the employee with nothing attached rather than
	// dropping the employee.
	want := "SELECT t0.`f_uid` AS `a.employee_uid`, t1.`f_uid` AS `b.project_uid` FROM {{.res_employee}} t0 " +
		"LEFT JOIN ({{.res_member}} t2 JOIN {{.res_project}} t1 ON t2.`project_code` = t1.`f_code` AND t2.`project_org_code` = t1.`f_org`) " +
		"ON t2.`employee_code` = t0.`f_code` AND t2.`employee_org_code` = t0.`f_org` AND t1.`f_code` IN ('P01') LIMIT 1000"
	if got := vega.request.Query; got != want {
		t.Fatalf("statement = %s\nwant      = %s", got, want)
	}
}

func TestIndirectRelationRowFilterFailureIsNotSkipped(t *testing.T) {
	permission := &stubPermission{rowFilterErr: errors.New("policy service unavailable")}
	_, err := indirectService(permission, &recordingVega{}).Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: "main",
		Query: "MATCH (a:employee)-[:participates_in]->(b:project) RETURN count(*) AS links",
	})
	if err == nil {
		t.Fatalf("a failed row-filter decision must fail the query")
	}
}

// A relation the caller may not query reads as unknown, and nothing reaches
// vega-backend.
func TestIndirectRelationDeniedRelationIsUnknown(t *testing.T) {
	vega := &recordingVega{}
	permission := &stubPermission{denied: map[string]bool{
		interfaces.KNChildResourceID("kn_1", "rt_participates"): true,
	}}
	_, err := indirectService(permission, vega).Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: "main",
		Query: "MATCH (a:employee)-[:participates_in]->(b:project) RETURN count(*) AS links",
	})
	if err == nil || !strings.Contains(err.Error(), "unknown relationship type") {
		t.Fatalf("err = %v, want the relation to read as unknown", err)
	}
	if vega.request != nil {
		t.Fatal("a denied relation still reached vega-backend")
	}
}

// The proxy reads through what the published model grants: the relation's
// query_data on its backing resource, besides its two ends.
func TestIndirectRelationProxyBindingsIncludeTheBackingResource(t *testing.T) {
	plan, err := compileIndirect(t, "MATCH (a:employee)-[:participates_in]->(b:project) RETURN count(*) AS links",
		CompileOptions{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	var got []string
	for _, binding := range proxyBindings(plan) {
		got = append(got, binding.ChildType+":"+binding.ChildID+"->"+binding.TargetID)
	}
	want := []string{
		interfaces.MODULE_TYPE_OBJECT_TYPE + ":ot_employee->res_employee",
		interfaces.MODULE_TYPE_OBJECT_TYPE + ":ot_project->res_project",
		interfaces.MODULE_TYPE_RELATION_TYPE + ":rt_participates->res_employee",
		interfaces.MODULE_TYPE_RELATION_TYPE + ":rt_participates->res_project",
		interfaces.MODULE_TYPE_RELATION_TYPE + ":rt_participates->res_member",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("bindings = %v\nwant       %v", got, want)
	}
	if got, want := planResourceIDs(plan), []string{"res_employee", "res_project", "res_member"}; !slices.Equal(got, want) {
		t.Fatalf("resources = %v, want %v", got, want)
	}
}

// Explain names the relation between the two nodes and reads nothing; the
// backing table is part of the relation, not an object of its own.
func TestIndirectRelationExplain(t *testing.T) {
	service := indirectService(&stubPermission{}, &recordingVega{})
	plan, vega, err := explainQuery(t, service, "MATCH (a:employee)-[:participates_in]->(b:project) RETURN a.employee_uid, b.project_uid")
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	if vega.request != nil {
		t.Fatal("explain read data")
	}
	var objects []string
	for _, object := range plan.Objects {
		objects = append(objects, object.Alias+":"+object.ObjectTypeID)
	}
	if want := []string{"a:ot_employee", "b:ot_project"}; !slices.Equal(objects, want) {
		t.Fatalf("objects = %v, want %v", objects, want)
	}
	if len(plan.Relations) != 1 || plan.Relations[0].RelationTypeID != "rt_participates" ||
		plan.Relations[0].Source != "a" || plan.Relations[0].Target != "b" {
		t.Fatalf("relations = %+v", plan.Relations)
	}
}

// The request #2653 reported, end to end through the service: it compiles,
// authorizes the properties it returns, and runs as one SQL statement.
func TestIndirectRelationGraphQueryRuns(t *testing.T) {
	vega := &recordingVega{}
	result, err := indirectService(&stubPermission{}, vega).Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: "main",
		Query:            "MATCH (a:employee)-[r:participates_in]->(b:project) RETURN a, r, b LIMIT 25",
		ObjectProperties: map[string][]string{"a": {"employee_name"}, "b": {"project_name"}},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if vega.request == nil || !strings.Contains(vega.request.Query, forwardLinks) {
		t.Fatalf("statement does not join through the membership row: %+v", vega.request)
	}
	var names []string
	for _, column := range result.Columns {
		names = append(names, column.Name)
	}
	if want := []string{"a", "r", "b"}; !slices.Equal(names, want) {
		t.Fatalf("columns = %v, want %v", names, want)
	}
}

func TestIndirectRelationPropertyIsRefusedClearly(t *testing.T) {
	_, err := compileIndirect(t, "MATCH (a:employee)-[r:participates_in]->(b:project) RETURN r.since", CompileOptions{})
	if err == nil {
		t.Fatal("a relationship property was accepted")
	}
}

// A relationship closing a cycle joins its backing table after both ends; with
// collect, the derived table of conclusions reads that backing table's columns
// (the relationship's basis), so the backing table has to be joined before it.
func TestIndirectRelationClosingACycleJoinsBeforeTheConclusions(t *testing.T) {
	for _, query := range []string{
		"MATCH (a:employee)-[m:mentors]->(a) RETURN m, collect(a.employee_name) AS names",
		"MATCH (a:employee)-[:participates_in]->(p:project)<-[:participates_in]-(b:employee), (a)-[m:mentors]->(b) " +
			"RETURN m, collect(p) AS projects",
	} {
		t.Run(query, func(t *testing.T) {
			plan, err := compileIndirect(t, query, CompileOptions{})
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			read, err := planGraphRead(plan, DialectMySQL, interfaces.CYPHER_DEFAULT_LIMIT)
			if err != nil {
				t.Fatalf("planGraphRead: %v", err)
			}
			statement := read.statement
			conclusions := strings.LastIndex(statement, ") c0 ON ")
			if conclusions < 0 {
				t.Fatalf("statement has no conclusions join: %s", statement)
			}
			outer := statement[:strings.Index(statement, "JOIN (SELECT")] + statement[conclusions:]
			backing := strings.Index(outer, "JOIN {{.res_mentoring}}")
			if backing < 0 || backing > strings.Index(outer, ") c0 ON ") {
				t.Fatalf("the backing table is joined after the conclusions that read it: %s", statement)
			}
		})
	}
}
