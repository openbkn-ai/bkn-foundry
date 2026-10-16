// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

type m19GraphFixture struct {
	m19Fixture
	AdapterStatus string `json:"adapter_status"`
	AdapterDetail string `json:"adapter_detail"`
	Graph         struct {
		Nodes []struct {
			Identity, Type string
			Properties     map[string]any
		}
		Edges []struct {
			Type           string
			Source, Target int
		}
	}
}
type m19SQLite struct {
	*recordingVega
	stdin        io.WriteCloser
	stdout       *bufio.Reader
	adapterError string
}

func (source *m19SQLite) exchange(request any) ([]byte, error) {
	raw, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	if _, err = source.stdin.Write(append(raw, '\n')); err != nil {
		return nil, err
	}
	raw, err = source.stdout.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	var status struct {
		AdapterError string `json:"adapter_error"`
	}
	if err = json.Unmarshal(raw, &status); err != nil {
		return nil, err
	}
	if status.AdapterError != "" {
		source.adapterError = status.AdapterError
		return nil, fmt.Errorf("SQLite fixture adapter: %s", status.AdapterError)
	}
	return raw, nil
}
func (source *m19SQLite) RawQuery(_ context.Context, request *interfaces.RawQueryRequest) (*interfaces.RawQueryResponse, error) {
	raw, err := source.exchange(map[string]any{"sql": request.Query})
	if err != nil {
		return nil, err
	}
	var result interfaces.RawQueryResponse
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err = decoder.Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}
func startM19SQLite(t *testing.T) *m19SQLite {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python stdlib sqlite3 is required for the independent graph fixture adapter")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	command := exec.CommandContext(ctx, python, "testdata/m19_sqlite.py")
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = input.Close(); _ = command.Wait() })
	return &m19SQLite{recordingVega: &recordingVega{}, stdin: input, stdout: bufio.NewReader(output)}
}

type m19SourceTable struct {
	Name        string           `json:"name"`
	Columns     []string         `json:"columns"`
	Rows        []map[string]any `json:"rows"`
	BoolColumns []string         `json:"bool_columns"`
	JSONColumns []string         `json:"json_columns"`
}

var m19PropertyNames = regexp.MustCompile(`\b[A-Za-z_][A-Za-z_0-9]*\.([A-Za-z_][A-Za-z_0-9]*)`)
var m19Labels = regexp.MustCompile(`:\s*([A-Za-z_][A-Za-z_0-9]*)`)

// The fixture model explicitly declares query properties. Native graph data
// has no schema; BKN does. Synthetic identity/FK fields stay internal, never
// become native expected properties, and cannot certify properties(n)/keys(n).
func m19GraphModel(test m19GraphFixture) (*fakeSchemaSource, []m19SourceTable, string) {
	source := &fakeSchemaSource{}
	fields := map[string]string{"__tck_identity": "string"}
	types := map[string]bool{"__tck_node": true}
	for _, node := range test.Graph.Nodes {
		types[node.Type] = true
		for name, value := range node.Properties {
			kind := expressionValueType(value)
			if _, ok := value.(json.Number); ok {
				kind = "integer"
				if strings.ContainsAny(value.(json.Number).String(), ".eE") {
					kind = "float"
				}
			}
			if previous, ok := fields[name]; ok && previous != kind {
				fields[name] = "any"
			} else {
				fields[name] = kind
			}
		}
	}
	for _, match := range m19PropertyNames.FindAllStringSubmatch(test.Query, -1) {
		if _, ok := fields[match[1]]; !ok {
			fields[match[1]] = "any"
		}
	}
	// A query may refer to an empty type. Its fixture model must still declare it.
	for _, match := range m19Labels.FindAllStringSubmatch(test.Query, -1) {
		types[match[1]] = true
	}
	if strings.Contains(test.Query, "labels(") {
		for _, node := range test.Graph.Nodes {
			if node.Type == "__tck_node" {
				return nil, nil, "unlabeled native node label observation differs from the typed BKN contract"
			}
		}
	}
	if strings.Contains(test.Query, "properties(") || strings.Contains(test.Query, "keys(") {
		return nil, nil, "native complete property view requires a separate snapshot-contract adapter"
	}
	rows := map[string][]map[string]any{}
	for _, node := range test.Graph.Nodes {
		row := map[string]any{"__tck_identity": node.Identity}
		for name, value := range node.Properties {
			row[name] = value
		}
		rows[node.Type] = append(rows[node.Type], row)
	}
	edgeTypes := map[string][]int{}
	for index, edge := range test.Graph.Edges {
		edgeTypes[edge.Type] = append(edgeTypes[edge.Type], index)
	}
	// Direct bindings have fixed endpoint types and scalar key mappings. Refuse
	// fixtures that cannot be represented; never replace their graph topology.
	for _, name := range sortedGraphKeys(edgeTypes) {
		indexes := edgeTypes[name]
		first := test.Graph.Edges[indexes[0]]
		left, right := test.Graph.Nodes[first.Source].Type, test.Graph.Nodes[first.Target].Type
		out, in := map[int]int{}, map[int]int{}
		pairs := map[[2]int]bool{}
		for _, index := range indexes {
			edge := test.Graph.Edges[index]
			if test.Graph.Nodes[edge.Source].Type != left || test.Graph.Nodes[edge.Target].Type != right {
				return nil, nil, "polymorphic relation endpoints require a distinct binding adapter"
			}
			pair := [2]int{edge.Source, edge.Target}
			if pairs[pair] {
				return nil, nil, "parallel relationship instances are outside the virtual graph contract"
			}
			pairs[pair] = true
			out[edge.Source]++
			in[edge.Target]++
		}
		sourceUnique, targetUnique := true, true
		for _, count := range out {
			sourceUnique = sourceUnique && count == 1
		}
		for _, count := range in {
			targetUnique = targetUnique && count == 1
		}
		if !sourceUnique && !targetUnique {
			return nil, nil, "many-to-many topology requires a backing relation resource adapter"
		}
		key := "__tck_fk_" + name
		fields[key] = "string"
		relation := relationType(name, name)
		relation.SourceObjectTypeID = left
		relation.TargetObjectTypeID = right
		sourceKey, targetKey := key, "__tck_identity"
		if !sourceUnique {
			sourceKey, targetKey = "__tck_identity", key
		}
		relation.MappingRules = []interfaces.Mapping{{SourceProp: interfaces.SimpleProperty{Name: sourceKey}, TargetProp: interfaces.SimpleProperty{Name: targetKey}}}
		source.relationTypes = append(source.relationTypes, relation)
		for _, index := range indexes {
			edge := test.Graph.Edges[index]
			from, to := edge.Source, edge.Target
			if !sourceUnique {
				from, to = to, from
			}
			identity := test.Graph.Nodes[from].Identity
			for _, row := range rows[test.Graph.Nodes[from].Type] {
				if row["__tck_identity"] == identity {
					row[key] = test.Graph.Nodes[to].Identity
				}
			}
		}
	}

	// The native fixture may omit an unused relationship type. Declare an empty
	// BKN binding for query references instead of changing unknown-model behavior.
	tree, parseError := Parse(test.Query)
	if parseError == nil {
		analyzed, analyzeError := AnalyzeGraph(tree)
		if analyzeError == nil {
			known := map[string]bool{}
			for _, relation := range source.relationTypes {
				known[relation.RTID] = true
			}
			labels := map[string]string{}
			var declare func(*Query)
			declare = func(query *Query) {
				for _, node := range query.Pattern.Nodes {
					if node.Label != "" {
						labels[node.Variable] = node.Label
					}
				}
				for _, edge := range query.Pattern.Edges {
					names := edge.Types
					if edge.Type != "" {
						names = []string{edge.Type}
					}
					for _, name := range names {
						if known[name] {
							continue
						}
						known[name] = true
						left, right := labels[query.Pattern.Nodes[edge.Left].Variable], labels[query.Pattern.Nodes[edge.Right].Variable]
						if left == "" {
							left = "__tck_node"
						}
						if right == "" {
							right = "__tck_node"
						}
						if edge.Direction == Incoming {
							left, right = right, left
						}
						types[left], types[right] = true, true
						key := "__tck_empty_" + name
						fields[key] = "string"
						relation := relationType(name, name)
						relation.SourceObjectTypeID = left
						relation.TargetObjectTypeID = right
						relation.MappingRules = []interfaces.Mapping{{SourceProp: interfaces.SimpleProperty{Name: key}, TargetProp: interfaces.SimpleProperty{Name: "__tck_identity"}}}
						source.relationTypes = append(source.relationTypes, relation)
					}
				}
				for _, stage := range query.Stages {
					declare(stage.Query)
				}
				for _, branch := range query.Union {
					declare(branch)
				}
			}
			declare(analyzed)
		}
	}
	tables := []m19SourceTable{}
	for _, name := range sortedGraphKeys(types) {
		columns := sortedGraphKeys(fields)
		props := []*interfaces.DataProperty{}
		propertyKinds := map[string]string{}
		for _, field := range columns {
			observed := map[string]bool{}
			for _, node := range test.Graph.Nodes {
				if node.Type != name || node.Properties[field] == nil {
					continue
				}
				value := node.Properties[field]
				kind := expressionValueType(value)
				if number, ok := value.(json.Number); ok {
					kind = "integer"
					if strings.ContainsAny(number.String(), ".eE") {
						kind = "float"
					}
				}
				observed[kind] = true
			}
			kind := fields[field]
			if len(observed) == 1 {
				kind = sortedGraphKeys(observed)[0]
			}
			if len(observed) > 1 {
				kind = "json"
			}
			if !interfaces.ValidDataPropertyTypes[kind] {
				kind = "string"
			}
			property := dataProperty(field, field)
			property.Type = kind
			propertyKinds[field] = kind
			props = append(props, property)
		}
		object := objectType(name, name, resource("tck_"+name, "tck_"+name), props...)
		object.PrimaryKeys = []string{"__tck_identity"}
		source.objectTypes = append(source.objectTypes, object)
		entries := rows[name]
		if entries == nil {
			entries = []map[string]any{}
		}
		booleans := []string{}
		for field, kind := range propertyKinds {
			if kind == "boolean" {
				booleans = append(booleans, field)
			}
		}
		jsonColumns := []string{}
		for field, kind := range propertyKinds {
			if kind == "json" {
				jsonColumns = append(jsonColumns, field)
			}
		}
		tables = append(tables, m19SourceTable{JSONColumns: jsonColumns, BoolColumns: booleans, Name: "tck_" + name, Columns: columns, Rows: entries})
	}
	return source, tables, ""
}

func TestM19GraphCompatibilityInventory(t *testing.T) {
	raw, err := os.ReadFile("testdata/tck_m19_graph.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		SHA256 string
		Cases  []m19GraphFixture
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err = decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.SHA256 != "93d3ea6dcf386a64754bb561936a7035c3b429f81b9bc091b809061b4ea0d9bd" || len(fixture.Cases) != 680 {
		t.Fatal("unexpected source")
	}
	sqlite := startM19SQLite(t)
	counts := map[string]int{}
	results := []m19Result{}
	failures := 0
	for _, test := range fixture.Cases {
		entry := m19Result{Source: test.Source, Scenario: test.Scenario, Example: test.Example}
		switch {
		case test.AdapterStatus != "candidate":
			entry.Status = "requires_fixture_adapter"
			entry.Detail = test.AdapterDetail
		default:
			schema, tables, reason := m19GraphModel(test)
			if reason != "" {
				entry.Status = "requires_graph_contract_adapter"
				entry.Detail = reason
				break
			}
			if _, err = sqlite.exchange(map[string]any{"tables": tables}); err != nil {
				t.Fatal(err)
			}
			assertM19FixtureTopology(t, sqlite, test, schema)
			sqlite.adapterError = ""
			service := &cypherQueryService{ps: &stubPermission{}, schema: schema, vba: sqlite}
			request := graphRequest(test.Query)
			request.Parameters = test.Parameters
			result, queryError := service.Query(context.Background(), request)
			if queryError != nil {
				entry.Status = "failed_query"
				entry.Detail = queryError.Error()
				if sqlite.adapterError != "" {
					entry.Status = "requires_sql_dialect_adapter"
					entry.Detail = sqlite.adapterError
				}
				if strings.Contains(entry.Detail, "Unsupported") || strings.Contains(entry.Detail, "not supported") {
					entry.Status = "unsupported"
				}
				if strings.Contains(entry.Detail, "LimitExceeded") {
					entry.Status = "resource_budget_not_compatible"
				}
				break
			}
			columns := []string{}
			for _, column := range result.Columns {
				columns = append(columns, column.Name)
			}
			got, gotErr := m19RowKeys(result.Entries, false, test.IgnoreListOrder)
			want, wantErr := m19RowKeys(test.Expected, true, test.IgnoreListOrder)
			if gotErr != nil || wantErr != nil {
				t.Fatal(gotErr, wantErr)
			}
			if !test.Ordered {
				sort.Strings(got)
				sort.Strings(want)
			}
			entry.Status = "passed_result"
			if !reflect.DeepEqual(columns, test.Columns) || !reflect.DeepEqual(got, want) {
				entry.Status = "failed_result"
				entry.Detail = fmt.Sprintf("columns=%v want=%v rows=%v want=%v", columns, test.Columns, got, want)
			}
		}
		counts[entry.Status]++
		results = append(results, entry)
		if strings.HasPrefix(entry.Status, "failed_") {
			failures++
			if failures <= 30 {
				t.Logf("%s %s example %d: %s\n%s\n%s", test.Source, test.Scenario, test.Example, entry.Status, entry.Detail, test.Query)
			}
		}
	}
	report := struct {
		SHA256 string
		Counts map[string]int
		Cases  []m19Result
	}{fixture.SHA256, counts, results}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := os.Getenv("BKN_M19_GRAPH_REPORT")
	if path == "" {
		path = filepath.Join(t.TempDir(), "m19-graph.json")
	}
	if err = os.WriteFile(path, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	assertM19VerifiedScenarios(t, "graph", results)
	t.Logf("M19 graph results: %v; report=%s", counts, path)
	if failures > 0 {
		t.Fatalf("%d unexpected graph mismatches", failures)
	}
}

// Check the adapter's synthetic FK model against the upstream input edges
// before running the product query. A wrong fixture cannot produce a pass
// merely because the expected result happens to be empty.
func assertM19FixtureTopology(t *testing.T, sqlite *m19SQLite, test m19GraphFixture, schema *fakeSchemaSource) {
	t.Helper()
	quote := func(value string) string { return `"` + strings.ReplaceAll(value, `"`, `""`) + `"` }
	for _, relation := range schema.relationTypes {
		wanted := map[string]int{}
		for _, edge := range test.Graph.Edges {
			if edge.Type == relation.RTID {
				wanted[test.Graph.Nodes[edge.Source].Identity+":"+test.Graph.Nodes[edge.Target].Identity]++
			}
		}
		mapping := relation.MappingRules.([]interfaces.Mapping)[0]
		query := "SELECT s.__tck_identity AS source,t.__tck_identity AS target FROM " + quote("tck_"+relation.SourceObjectTypeID) + " s JOIN " + quote("tck_"+relation.TargetObjectTypeID) + " t ON s." + quote(mapping.SourceProp.Name) + "=t." + quote(mapping.TargetProp.Name)
		raw, err := sqlite.exchange(map[string]any{"sql": query})
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Entries []struct{ Source, Target string }
		}
		if err = json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		actual := map[string]int{}
		for _, edge := range result.Entries {
			actual[edge.Source+":"+edge.Target]++
		}
		if !reflect.DeepEqual(actual, wanted) {
			t.Fatalf("input graph topology changed for %s %s: %v != %v", test.Source, relation.RTID, actual, wanted)
		}
	}
}

func TestM19FixtureToolIntegrity(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python stdlib is required to verify the independent fixture tools")
	}
	command := exec.Command(python, "-m", "unittest", "discover", "-s", "testdata", "-p", "test_*.py")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("fixture integrity checks: %v\n%s", err, output)
	}
}
