// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// A network built from a knowledge base keeps its objects and the link tables
// of its relations in datasets (bkn-foundry issue 3283). Its relations through
// a link table are indirect, and SQL cannot read a dataset, so the bind-join
// executor has to read them: every MATCH across one was refused before.
func indirectDatasetService(vega *datasetVega) *cypherQueryService {
	return &cypherQueryService{
		ps:     &stubPermission{},
		schema: &fakeSchemaSource{objectTypes: indirectObjectTypes(), relationTypes: indirectRelationTypes()},
		vba:    vega,
		appSetting: &common.AppSetting{ServerSetting: common.ServerSetting{
			CypherExecutor: common.CypherExecutorSQL,
		}},
	}
}

// Each side of participates_in has two keys, code and org. Ann (E1 in org A)
// and Cid (E1 in org B) share a code, so a link row joined on one key of a
// side would credit either with the other's projects.
func indirectDatasetData() map[string][]map[string]any {
	return map[string][]map[string]any{
		"res_employee": {
			{"f_uid": "e1", "f_code": "E1", "f_org": "A", "f_name": "Ann"},
			{"f_uid": "e2", "f_code": "E2", "f_org": "A", "f_name": "Bob"},
			{"f_uid": "e3", "f_code": "E1", "f_org": "B", "f_name": "Cid"},
		},
		"res_project": {
			{"f_uid": "p1", "f_code": "P1", "f_org": "A", "f_name": "Apollo"},
			{"f_uid": "p2", "f_code": "P2", "f_org": "A", "f_name": "Borealis"},
			{"f_uid": "p3", "f_code": "P1", "f_org": "B", "f_name": "Cobalt"},
		},
		"res_member": {
			{"employee_code": "E1", "employee_org_code": "A", "project_code": "P1", "project_org_code": "A"},
			{"employee_code": "E1", "employee_org_code": "A", "project_code": "P2", "project_org_code": "A"},
			{"employee_code": "E1", "employee_org_code": "A", "project_code": "P1", "project_org_code": "B"},
			{"employee_code": "E2", "employee_org_code": "A", "project_code": "P1", "project_org_code": "A"},
			{"employee_code": "E1", "employee_org_code": "B", "project_code": "P1", "project_org_code": "B"},
			// Links whose employee or project is not there link nothing.
			{"employee_code": "E2", "employee_org_code": "B", "project_code": "P1", "project_org_code": "A"},
			{"employee_code": "E2", "employee_org_code": "A", "project_code": "P9", "project_org_code": "A"},
		},
		"res_mentoring": {
			{"mentor_uid": "e1", "mentee_uid": "e2"},
			{"mentor_uid": "e2", "mentee_uid": "e3"},
		},
	}
}

func queryIndirectDataset(t *testing.T, query string) (*interfaces.CypherQueryResult, *datasetVega, error) {
	t.Helper()
	vega := &datasetVega{accountDataAPI: accountDataAPI{api: newFakeDataAPI(indirectDatasetData())}}
	result, err := indirectDatasetService(vega).Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: interfaces.MAIN_BRANCH, Query: query,
	})
	return result, vega, err
}

func entryStrings(entries []map[string]any, columns ...string) []string {
	rows := make([]string, 0, len(entries))
	for _, entry := range entries {
		values := make([]string, 0, len(columns))
		for _, column := range columns {
			values = append(values, fmt.Sprint(entry[column]))
		}
		rows = append(rows, strings.Join(values, "|"))
	}
	return rows
}

func TestQueryOutsideSQLMatchesThroughALinkTable(t *testing.T) {
	spans := recordSpans(t)
	for _, tc := range []struct {
		name    string
		query   string
		columns []string
		want    []string
	}{
		{
			name:    "both ends",
			query:   "MATCH (a:employee)-[:participates_in]->(b:project) RETURN a.employee_name AS e, b.project_name AS p ORDER BY e, p",
			columns: []string{"e", "p"},
			want:    []string{"Ann|Apollo", "Ann|Borealis", "Ann|Cobalt", "Bob|Apollo", "Cid|Cobalt"},
		},
		{
			name:    "against the relation's direction, from a filtered end",
			query:   "MATCH (b:project)<-[:participates_in]-(a:employee) WHERE b.project_name = 'Apollo' RETURN a.employee_name AS e ORDER BY e",
			columns: []string{"e"},
			want:    []string{"Ann", "Bob"},
		},
		{
			name:    "counted per group",
			query:   "MATCH (a:employee)-[:participates_in]->(b:project) RETURN b.project_name AS p, count(a) AS n ORDER BY p",
			columns: []string{"p", "n"},
			want:    []string{"Apollo|2", "Borealis|1", "Cobalt|2"},
		},
		{
			name: "two link tables in a row",
			query: "MATCH (m:employee)-[:mentors]->(x:employee)-[:participates_in]->(b:project) " +
				"RETURN m.employee_name AS m, x.employee_name AS x, b.project_name AS p ORDER BY m, x, p",
			columns: []string{"m", "x", "p"},
			want:    []string{"Ann|Bob|Apollo", "Bob|Cid|Cobalt"},
		},
		{
			// The last link row joins two rows already bound.
			name: "closing a cycle",
			query: "MATCH (a:employee)-[:mentors]->(x:employee), (a)-[:participates_in]->(b:project)<-[:participates_in]-(x) " +
				"RETURN a.employee_name AS a, x.employee_name AS x, b.project_name AS p",
			columns: []string{"a", "x", "p"},
			want:    []string{"Ann|Bob|Apollo"},
		},
		{
			name:    "undirected",
			query:   "MATCH (a:employee)-[:participates_in]-(b:project) WHERE a.employee_name = 'Cid' RETURN b.project_name AS p",
			columns: []string{"p"},
			want:    []string{"Cobalt"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, vega, err := queryIndirectDataset(t, tc.query)
			if err != nil {
				t.Fatalf("Query: %v", err)
			}
			if got := entryStrings(result.Entries, tc.columns...); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("entries = %v, want %v", got, tc.want)
			}
			if vega.statements != 1 || len(vega.api.readsOf("res_member")) == 0 {
				t.Errorf("statements = %d, link reads = %d; want one SQL attempt, then the link table read",
					vega.statements, len(vega.api.readsOf("res_member")))
			}
			if spans.attrs["cypher.executor"] != "bindjoin" || spans.attrs["cypher.fallback"] != "non_sql_source" {
				t.Errorf("span attrs = %v", spans.attrs)
			}
		})
	}
}

// A relationship over a link table comes back as one, from the link row that
// matched.
func TestQueryOutsideSQLReturnsARelationshipThroughALinkTable(t *testing.T) {
	result, _, err := queryIndirectDataset(t,
		"MATCH (a:employee)-[r:participates_in]->(b:project) WHERE a.employee_name = 'Bob' RETURN a, r, b")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(result.Entries) != 1 {
		t.Fatalf("entries = %v, want Bob's one link", result.Entries)
	}
	body, _ := json.Marshal(result.Entries)
	for _, part := range []string{`"kind":"relation"`, `"relation_type_id":"rt_participates"`,
		`"object_type_id":"ot_employee"`, `"object_type_id":"ot_project"`} {
		if !strings.Contains(string(body), part) {
			t.Errorf("entries = %s, want %s", body, part)
		}
	}
}

// An OPTIONAL MATCH over a link table has to find the link row and its far end
// together, which only SQL does; the refusal names the relation.
func TestQueryOutsideSQLRefusesAnOptionalMatchThroughALinkTable(t *testing.T) {
	_, vega, err := queryIndirectDataset(t,
		"MATCH (a:employee) OPTIONAL MATCH (a)-[:participates_in]->(b:project) RETURN a.employee_name AS e, count(b.project_uid) AS n")
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusBadRequest ||
		httpErr.ErrorCode != "BknBackend.Cypher.Unsupported" {
		t.Fatalf("err = %v", err)
	}
	details, _ := httpErr.ErrorDetails.(string)
	if !strings.Contains(details, "participates_in") || !strings.Contains(details, "OPTIONAL MATCH") {
		t.Errorf("details = %q, want the relation and OPTIONAL MATCH named", details)
	}
	if len(vega.readers) != 0 {
		t.Errorf("read the data API, %v", vega.readers)
	}
}
