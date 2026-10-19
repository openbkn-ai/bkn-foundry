// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// fakeDataAPI stands in for vega-backend's resource data API: it evaluates a
// filter_condition tree over in-memory rows, projects output_fields, sorts and
// pages with a cursor, and records every read.
type fakeDataAPI struct {
	mu      sync.Mutex
	tables  map[string][]map[string]any
	pending map[string][]map[string]any
	reads   []fakeRead
	refuse  map[string]bool
	cursors int
}

type fakeRead struct {
	resourceID string
	params     *interfaces.ResourceDataQueryParams
}

func newFakeDataAPI(tables map[string][]map[string]any) *fakeDataAPI {
	return &fakeDataAPI{tables: tables, pending: map[string][]map[string]any{}, refuse: map[string]bool{}}
}

func (f *fakeDataAPI) read(_ context.Context, resourceID string,
	params *interfaces.ResourceDataQueryParams) (*interfaces.DatasetQueryResponse, error) {
	// The executor reads batches concurrently.
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads = append(f.reads, fakeRead{resourceID: resourceID, params: params})
	if f.refuse[resourceID] {
		return nil, interfaces.NewDependencyError("vega", "resource_data", "", http.StatusForbidden)
	}
	if cursor := params.Paging.Cursor; cursor != "" {
		if params.FilterCondition != nil || params.OutputFields != nil || params.Sort != nil {
			return nil, fmt.Errorf("cursor continuation must contain only paging.cursor")
		}
		return f.page(f.pending[cursor], 0), nil
	}
	if params.IgnoreLocalIndex == nil || !*params.IgnoreLocalIndex {
		return nil, fmt.Errorf("the executor must read the data source, not a local index")
	}
	var matched []map[string]any
	for _, row := range f.tables[resourceID] {
		keep, err := fakeFilter(params.FilterCondition, row)
		if err != nil {
			return nil, err
		}
		if keep {
			projected := map[string]any{}
			for _, field := range params.OutputFields {
				projected[field] = jsonValue(row[field])
			}
			matched = append(matched, projected)
		}
	}
	sort.SliceStable(matched, func(i, j int) bool {
		for _, s := range params.Sort {
			if c := sortCompare(matched[i][s.Field], matched[j][s.Field]); c != 0 {
				if s.Direction == "desc" {
					return c > 0
				}
				return c < 0
			}
		}
		return false
	})
	return f.page(matched, params.Paging.Limit), nil
}

// page returns the first page of rows and parks the rest behind a cursor.
func (f *fakeDataAPI) page(rows []map[string]any, limit int) *interfaces.DatasetQueryResponse {
	if limit <= 0 {
		limit = 2
	}
	if len(rows) <= limit {
		return &interfaces.DatasetQueryResponse{Entries: rows, Paging: &interfaces.ResourceDataPagingResult{}}
	}
	f.cursors++
	cursor := "c" + strconv.Itoa(f.cursors)
	f.pending[cursor] = rows[limit:]
	return &interfaces.DatasetQueryResponse{Entries: rows[:limit],
		Paging: &interfaces.ResourceDataPagingResult{NextCursor: &cursor}}
}

// readsOf lists the first-page reads of one resource.
func (f *fakeDataAPI) readsOf(resourceID string) []*interfaces.ResourceDataQueryParams {
	var params []*interfaces.ResourceDataQueryParams
	for _, r := range f.reads {
		if r.resourceID == resourceID && r.params.Paging.Cursor == "" {
			params = append(params, r.params)
		}
	}
	return params
}

// jsonValue returns a number the way vega-backend's decoded response holds it.
func jsonValue(value any) any {
	switch v := value.(type) {
	case int:
		return json.Number(strconv.Itoa(v))
	case float64:
		return json.Number(strconv.FormatFloat(v, 'g', -1, 64))
	}
	return value
}

// fakeFilter evaluates a filter_condition the way vega-backend writes it as
// SQL: a comparison with NULL never holds.
func fakeFilter(condition map[string]any, row map[string]any) (bool, error) {
	if condition == nil {
		return true, nil
	}
	operation, _ := condition["operation"].(string)
	switch operation {
	case "and", "or":
		subConditions, _ := condition["sub_conditions"].([]map[string]any)
		if len(subConditions) > maxSubConditions {
			return false, fmt.Errorf("the number of subConditions exceeds %d", maxSubConditions)
		}
		for _, sub := range subConditions {
			holds, err := fakeFilter(sub, row)
			if err != nil {
				return false, err
			}
			if operation == "and" && !holds {
				return false, nil
			}
			if operation == "or" && holds {
				return true, nil
			}
		}
		return operation == "and", nil
	}
	field, _ := condition["field"].(string)
	value := row[field]
	switch operation {
	case "null":
		return value == nil, nil
	case "not_null":
		return value != nil, nil
	}
	if value == nil {
		return false, nil
	}
	operand := condition["value"]
	switch operation {
	case "==", "!=", "<", "<=", ">", ">=":
		order, ok := compareValues(value, operand)
		if !ok {
			return false, nil
		}
		return map[string]bool{"==": order == 0, "!=": order != 0, "<": order < 0, "<=": order <= 0,
			">": order > 0, ">=": order >= 0}[operation], nil
	case "in", "not_in":
		found := false
		for _, candidate := range reflectList(operand) {
			if order, ok := compareValues(value, candidate); ok && order == 0 {
				found = true
			}
		}
		return found == (operation == "in"), nil
	case "like", "not_like":
		literal, err := fakeLikeLiteral(operand.(string))
		if err != nil {
			return false, err
		}
		return strings.Contains(fmt.Sprint(value), literal) == (operation == "like"), nil
	case "prefix", "not_prefix":
		return strings.HasPrefix(fmt.Sprint(value), operand.(string)) == (operation == "prefix"), nil
	default:
		return false, fmt.Errorf("fake data API does not support %q", operation)
	}
}

// fakeLikeLiteral reads a like value the way vega-backend does: a backslash
// escapes the next character and a bare % is refused.
func fakeLikeLiteral(value string) (string, error) {
	var literal strings.Builder
	escaped := false
	for _, r := range value {
		switch {
		case escaped:
			literal.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case r == '%':
			return "", fmt.Errorf("like does not support the wildcard %%")
		default:
			literal.WriteRune(r)
		}
	}
	return literal.String(), nil
}

func reflectList(value any) []any {
	v := reflect.ValueOf(value)
	list := make([]any, v.Len())
	for i := range list {
		list[i] = v.Index(i).Interface()
	}
	return list
}

// bindJoinSchema is a model with typed properties, so arithmetic and
// aggregates plan: orders placed by customers on a composite key, items
// belonging to orders, and orders following one another.
func bindJoinSchema(t *testing.T) *Schema {
	t.Helper()
	typed := func(name, column, kind string) *interfaces.DataProperty {
		p := dataProperty(name, column)
		p.Type = kind
		return p
	}
	order := objectType("ot_order", "Order", resource("res_order", "orders"),
		typed("id", "f_id", "integer"),
		typed("previous_id", "f_prev", "integer"),
		typed("customer_code", "f_cust", "string"),
		typed("region", "f_region", "string"),
		typed("amount", "f_amount", "integer"),
		typed("placed_at", "f_placed", "datetime"),
	)
	order.PrimaryKeys = []string{"id"}
	customer := objectType("ot_customer", "Customer", resource("res_customer", "customers"),
		typed("code", "f_code", "string"),
		typed("region", "f_region", "string"),
		typed("name", "f_name", "string"),
	)
	customer.PrimaryKeys = []string{"code", "region"}
	item := objectType("ot_item", "Item", resource("res_item", "items"),
		typed("id", "f_id", "integer"),
		typed("order_id", "f_order", "integer"),
	)
	item.PrimaryKeys = []string{"id"}

	placedBy := relationType("rt_placed_by", "PLACED_BY")
	placedBy.MappingRules = []interfaces.Mapping{
		{SourceProp: interfaces.SimpleProperty{Name: "customer_code"}, TargetProp: interfaces.SimpleProperty{Name: "code"}},
		{SourceProp: interfaces.SimpleProperty{Name: "region"}, TargetProp: interfaces.SimpleProperty{Name: "region"}},
	}
	belongsTo := relationType("rt_belongs_to", "BELONGS_TO")
	belongsTo.SourceObjectTypeID, belongsTo.TargetObjectTypeID = "ot_item", "ot_order"
	belongsTo.MappingRules = []interfaces.Mapping{
		{SourceProp: interfaces.SimpleProperty{Name: "order_id"}, TargetProp: interfaces.SimpleProperty{Name: "id"}},
	}
	follows := relationType("rt_follows", "FOLLOWS")
	follows.SourceObjectTypeID, follows.TargetObjectTypeID = "ot_order", "ot_order"
	follows.MappingRules = []interfaces.Mapping{
		{SourceProp: interfaces.SimpleProperty{Name: "id"}, TargetProp: interfaces.SimpleProperty{Name: "previous_id"}},
	}
	return testSchema(t, &fakeSchemaSource{
		objectTypes:   []*interfaces.ObjectType{order, customer, item},
		relationTypes: []*interfaces.RelationType{placedBy, belongsTo, follows},
	})
}

func bindJoinData() map[string][]map[string]any {
	return map[string][]map[string]any{
		"res_order": {
			{"f_id": 1, "f_prev": nil, "f_cust": "c1", "f_region": "east", "f_amount": 50, "f_placed": "2024-01-01T00:00:00Z"},
			{"f_id": 2, "f_prev": 1, "f_cust": "c1", "f_region": "west", "f_amount": 70, "f_placed": "2024-01-31T23:59:59Z"},
			{"f_id": 3, "f_prev": 2, "f_cust": "c2", "f_region": "east", "f_amount": 20, "f_placed": "2024-02-01T00:00:00Z"},
			{"f_id": 4, "f_prev": nil, "f_cust": "c2", "f_region": nil, "f_amount": nil, "f_placed": nil},
		},
		"res_customer": {
			{"f_code": "c1", "f_region": "east", "f_name": "Alice"},
			{"f_code": "c1", "f_region": "west", "f_name": "Alicia"},
			{"f_code": "c2", "f_region": "east", "f_name": "Bob"},
			{"f_code": "c2", "f_region": "west", "f_name": "Bea"},
		},
		"res_item": {
			{"f_id": 11, "f_order": 1},
			{"f_id": 12, "f_order": 1},
			{"f_id": 13, "f_order": 2},
			{"f_id": 14, "f_order": nil},
		},
	}
}

func compileBindJoin(t *testing.T, query string, parameters map[string]any) (*Plan, *Schema) {
	t.Helper()
	tree, err := Parse(query)
	if err != nil {
		t.Fatalf("Parse(%q): %v", query, err)
	}
	analyzed, err := Analyze(tree)
	if err != nil {
		t.Fatalf("Analyze(%q): %v", query, err)
	}
	schema := bindJoinSchema(t)
	plan, err := Compile(analyzed, schema, CompileOptions{Parameters: parameters})
	if err != nil {
		t.Fatalf("Compile(%q): %v", query, err)
	}
	return plan, schema
}

// runFake runs a query through the bind-join executor against the fake data
// API and returns its rows, each as its columns in RETURN order.
func runFake(t *testing.T, api *fakeDataAPI, limits bindJoinLimits, query string,
	parameters map[string]any) ([][]any, error) {
	t.Helper()
	plan, schema := compileBindJoin(t, query, parameters)
	_, entries, err := runBindJoin(context.Background(), plan, schema, api.read, limits, interfaces.CYPHER_DEFAULT_LIMIT)
	if err != nil {
		return nil, err
	}
	rows := make([][]any, 0, len(entries))
	for _, entry := range entries {
		row := make([]any, 0, len(plan.Select))
		for _, column := range plan.Select {
			row = append(row, plainValue(entry[column.Alias]))
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// plainValue turns a json.Number into an int or a float64 for comparison.
func plainValue(value any) any {
	if n, ok := value.(json.Number); ok {
		if i, err := n.Int64(); err == nil {
			return int(i)
		}
		f, _ := n.Float64()
		return f
	}
	return value
}

func mustRunFake(t *testing.T, query string, parameters map[string]any) ([][]any, *fakeDataAPI) {
	t.Helper()
	api := newFakeDataAPI(bindJoinData())
	rows, err := runFake(t, api, defaultBindJoinLimits, query, parameters)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return rows, api
}

func TestBindJoinResults(t *testing.T) {
	for _, tc := range []struct {
		name       string
		query      string
		parameters map[string]any
		want       [][]any
	}{
		{
			name:  "one hop, condition on the far side",
			query: "MATCH (i:Item)-[:BELONGS_TO]->(o:Order) WHERE o.region = 'east' RETURN i.id AS item, o.id AS ord ORDER BY item",
			want:  [][]any{{11, 1}, {12, 1}},
		},
		{
			name:  "composite key joins whole tuples only",
			query: "MATCH (o:Order)-[:PLACED_BY]->(c:Customer) RETURN o.id AS id, c.name AS name ORDER BY id",
			want:  [][]any{{1, "Alice"}, {2, "Alicia"}, {3, "Bob"}},
		},
		{
			name: "optional match keeps unmatched rows with nulls",
			query: "MATCH (o:Order) OPTIONAL MATCH (o)<-[:BELONGS_TO]-(i:Item) " +
				"RETURN o.id AS ord, i.id AS item ORDER BY ord, item",
			want: [][]any{{1, 11}, {1, 12}, {2, 13}, {3, nil}, {4, nil}},
		},
		{
			name:  "optional match condition narrows what attaches, not the result",
			query: "MATCH (o:Order) OPTIONAL MATCH (o)<-[:BELONGS_TO]-(i:Item {id: 12}) RETURN o.id AS ord, i.id AS item ORDER BY ord",
			want:  [][]any{{1, 12}, {2, nil}, {3, nil}, {4, nil}},
		},
		{
			name:  "undirected relationship matches both readings once",
			query: "MATCH (a:Order)-[:FOLLOWS]-(b:Order) WHERE a.id = 2 RETURN b.id AS id ORDER BY id",
			want:  [][]any{{1}, {3}},
		},
		{
			name:  "a chain never reuses a relationship",
			query: "MATCH (a:Order)-[:FOLLOWS]->(b:Order)-[:FOLLOWS]->(c:Order) RETURN a.id, b.id, c.id",
			want:  [][]any{{1, 2, 3}},
		},
		{
			name:  "grouped aggregates, null group sorted first",
			query: "MATCH (o:Order) RETURN o.region AS region, count(*) AS n, sum(o.amount) AS total ORDER BY region",
			want:  [][]any{{nil, 1, nil}, {"east", 2, 70}, {"west", 1, 70}},
		},
		{
			name:  "aggregating nothing still returns one row",
			query: "MATCH (o:Order) WHERE o.region = 'north' RETURN count(*) AS n, sum(o.amount) AS s",
			want:  [][]any{{0, nil}},
		},
		{
			name:  "avg min max",
			query: "MATCH (o:Order) RETURN min(o.amount) AS lo, max(o.amount) AS hi, avg(o.amount) * 3.0 AS triple",
			want:  [][]any{{20, 70, 140.0}},
		},
		{
			name:  "count distinct across a join",
			query: "MATCH (i:Item)-[:BELONGS_TO]->(o:Order) RETURN count(DISTINCT o.id) AS orders, count(i.id) AS items",
			want:  [][]any{{2, 3}},
		},
		{
			name:  "ends with is evaluated in memory",
			query: "MATCH (c:Customer) WHERE c.name ENDS WITH 'ice' RETURN c.name",
			want:  [][]any{{"Alice"}},
		},
		{
			name:  "arithmetic comparison, null is unknown",
			query: "MATCH (o:Order) WHERE o.amount * 2 > 100 RETURN o.id",
			want:  [][]any{{2}},
		},
		{
			name:  "negation excludes null like SQL",
			query: "MATCH (o:Order) WHERE NOT o.region = 'east' RETURN o.id",
			want:  [][]any{{2}},
		},
		{
			name:  "distinct, skip and limit",
			query: "MATCH (c:Customer) RETURN DISTINCT c.code AS code ORDER BY code SKIP 1 LIMIT 1",
			want:  [][]any{{"c2"}},
		},
		{
			name:  "in an empty list matches nothing",
			query: "MATCH (o:Order) WHERE o.region IN [] RETURN o.id",
			want:  [][]any{},
		},
		{
			name:  "not in an empty list matches everything",
			query: "MATCH (o:Order) WHERE NOT o.region IN [] RETURN o.id ORDER BY o.id",
			want:  [][]any{{1}, {2}, {3}, {4}},
		},
		{
			name:       "list parameter",
			query:      "MATCH (i:Item)-[:BELONGS_TO]->(o:Order) WHERE o.id IN $ids RETURN i.id ORDER BY i.id",
			parameters: map[string]any{"ids": []any{2, 3}},
			want:       [][]any{{13}},
		},
		{
			name:  "descending order puts null last",
			query: "MATCH (o:Order) RETURN o.amount AS a ORDER BY a DESC",
			want:  [][]any{{70}, {50}, {20}, {nil}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := mustRunFake(t, tc.query, tc.parameters)
			if tc.want == nil {
				tc.want = [][]any{}
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("%s\n got %v\nwant %v", tc.query, got, tc.want)
			}
		})
	}
}

func TestBindJoinPushesConditionsToTheDataSource(t *testing.T) {
	_, api := mustRunFake(t,
		"MATCH (i:Item)-[:BELONGS_TO]->(o:Order) WHERE o.region = 'east' AND NOT o.amount < 30 RETURN i.id", nil)

	orders := api.readsOf("res_order")
	if len(orders) != 1 {
		t.Fatalf("orders read %d times, want once", len(orders))
	}
	want := map[string]any{"operation": "and", "sub_conditions": []map[string]any{
		fieldCondition("f_region", "==", "east"),
		fieldCondition("f_amount", ">=", int64(30)),
	}}
	if !reflect.DeepEqual(orders[0].FilterCondition, want) {
		t.Fatalf("order filter = %v, want %v", orders[0].FilterCondition, want)
	}
	// The order side is the narrower start, so items are read by key.
	items := api.readsOf("res_item")
	if len(items) != 1 || !reflect.DeepEqual(items[0].FilterCondition, fieldCondition("f_order", "in", []any{int64(1)})) {
		t.Fatalf("item reads = %v, want one keyed on the order found", items)
	}
}

func TestBindJoinBatchesAndPages(t *testing.T) {
	query := "MATCH (o:Order)-[:PLACED_BY]->(c:Customer) RETURN o.id AS id, c.name AS name ORDER BY id"
	reference, _ := mustRunFake(t, query, nil)

	api := newFakeDataAPI(bindJoinData())
	got, err := runFake(t, api, bindJoinLimits{batchSize: 1, pageSize: 1, maxRows: 100, workers: 2}, query, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, reference) {
		t.Fatalf("batched result %v differs from %v", got, reference)
	}
	if keyed := len(api.readsOf("res_customer")); keyed < 3 {
		t.Fatalf("customers read in %d batches, want one per key", keyed)
	}
	continued := 0
	for _, r := range api.reads {
		if r.params.Paging.Cursor != "" {
			continued++
		}
	}
	if continued == 0 {
		t.Fatal("no read followed a cursor, so paging was not exercised")
	}
}

func TestBindJoinRefusesToReadPastItsBudget(t *testing.T) {
	api := newFakeDataAPI(bindJoinData())
	_, err := runFake(t, api, bindJoinLimits{batchSize: 10, pageSize: 10, maxRows: 3, workers: 1},
		"MATCH (o:Order) RETURN o.id", nil)
	var budget *ReadBudgetError
	if !errors.As(err, &budget) || budget.Limit != 3 {
		t.Fatalf("err = %v, want a read budget error at 3 rows", err)
	}
}

func TestBindJoinReportsARefusal(t *testing.T) {
	api := newFakeDataAPI(bindJoinData())
	api.refuse["res_customer"] = true
	_, err := runFake(t, api, defaultBindJoinLimits,
		"MATCH (o:Order)-[:PLACED_BY]->(c:Customer) RETURN c.name", nil)
	if !vegaRefused(err) {
		t.Fatalf("err = %v, want vega's refusal so the service can try the proxy", err)
	}
}

func TestBindJoinSkipsReadingWhenNothingCanMatch(t *testing.T) {
	_, api := mustRunFake(t, "MATCH (i:Item)-[:BELONGS_TO]->(o:Order) WHERE o.region IN [] RETURN i.id", nil)
	if len(api.reads) != 0 {
		t.Fatalf("read %d pages for a query nothing can match", len(api.reads))
	}
}

func TestPushFilter(t *testing.T) {
	east := PlanCondition{Column: "f_region", Operator: "=", Value: Literal{Kind: LiteralString, String: "east"}}
	big := PlanCondition{Column: "f_amount", Operator: ">", Value: Literal{Kind: LiteralInteger, Integer: 10}}
	for _, tc := range []struct {
		name      string
		predicate PlanPredicate
		want      map[string]any
		constant  *bool
		refused   bool
	}{
		{name: "comparison", predicate: east, want: fieldCondition("f_region", "==", "east")},
		{name: "negated comparison inverts", predicate: PlanNegation{Operand: big},
			want: fieldCondition("f_amount", "<=", int64(10))},
		{name: "de morgan", predicate: PlanNegation{Operand: PlanLogical{Operator: "AND", Operands: []PlanPredicate{east, big}}},
			want: map[string]any{"operation": "or", "sub_conditions": []map[string]any{
				fieldCondition("f_region", "!=", "east"), fieldCondition("f_amount", "<=", int64(10))}}},
		{name: "null check", predicate: PlanNegation{Operand: PlanNullCheck{Column: "f_region"}},
			want: map[string]any{"field": "f_region", "operation": "not_null"}},
		{name: "starts with", predicate: PlanStringMatch{Column: "f_name", Operator: StartsWith, Value: "Al"},
			want: fieldCondition("f_name", "prefix", "Al")},
		{name: "negated contains", predicate: PlanNegation{Operand: PlanStringMatch{Column: "f_name", Operator: Contains, Value: "l"}},
			want: fieldCondition("f_name", "not_like", "l")},
		{name: "ends with stays in memory", predicate: PlanStringMatch{Column: "f_name", Operator: EndsWith, Value: "e"}, refused: true},
		{name: "xor stays in memory", predicate: PlanLogical{Operator: "XOR", Operands: []PlanPredicate{east, big}}, refused: true},
		{name: "column comparison stays in memory", predicate: PlanColumnComparison{Operator: "="}, refused: true},
		{name: "false decides an and", predicate: PlanLogical{Operator: "AND", Operands: []PlanPredicate{east, PlanNever{}}},
			constant: &alwaysFalse},
		{name: "never under not is always", predicate: PlanNegation{Operand: PlanNever{}}, constant: &alwaysTrue},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pushed, ok := pushFilter(tc.predicate)
			if tc.refused {
				if ok {
					t.Fatalf("pushed %v, want it left to the executor", pushed)
				}
				return
			}
			if !ok {
				t.Fatal("not pushed")
			}
			if tc.constant != nil {
				if pushed.constant == nil || *pushed.constant != *tc.constant {
					t.Fatalf("got %+v, want constant %v", pushed, *tc.constant)
				}
				return
			}
			if !reflect.DeepEqual(pushed.condition, tc.want) {
				t.Fatalf("got %v, want %v", pushed.condition, tc.want)
			}
		})
	}
}

func TestIntegerArithmeticOverflows(t *testing.T) {
	if _, err := integerArithmetic("*", 1<<32, 1<<32); !errors.Is(err, errIntegerOverflow) {
		t.Fatalf("2^64 did not overflow: %v", err)
	}
	if got, err := integerArithmetic("*", -(1 << 62), 2); err != nil || got != -(1<<63) {
		t.Fatalf("-2^63 = %d, %v", got, err)
	}
	if _, err := integerArithmetic("+", 1<<62, 1<<62); !errors.Is(err, errIntegerOverflow) {
		t.Fatalf("2^63 did not overflow: %v", err)
	}
}

// accountDataAPI serves the fake data API to every account except the ones it
// refuses, recording who read what.
type accountDataAPI struct {
	interfaces.VegaBackendAccess
	mu          sync.Mutex
	sqlRequest  *interfaces.RawQueryRequest
	sqlResponse *interfaces.RawQueryResponse
	api         *fakeDataAPI
	refused     map[string]bool
	readers     []string
}

func (v *accountDataAPI) QueryResourceDataAs(ctx context.Context, account interfaces.AccountInfo, resourceID string,
	params *interfaces.ResourceDataQueryParams) (*interfaces.DatasetQueryResponse, error) {
	v.mu.Lock()
	v.readers = append(v.readers, account.ID)
	v.mu.Unlock()
	if v.refused[account.ID] {
		return nil, interfaces.NewDependencyError("vega", "resource_data", interfaces.DependencyForbidden, http.StatusForbidden)
	}
	return v.api.read(ctx, resourceID, params)
}

// RawQuery serves the SQL fallback.
func (v *accountDataAPI) RawQuery(_ context.Context, req *interfaces.RawQueryRequest) (*interfaces.RawQueryResponse, error) {
	v.sqlRequest = req
	if v.sqlResponse != nil {
		return v.sqlResponse, nil
	}
	return &interfaces.RawQueryResponse{}, nil
}

func bindJoinService(vega interfaces.VegaBackendAccess, proxies interfaces.KNProxyBindingResolver) *cypherQueryService {
	service := relationService(nil, &stubPermission{}, proxies)
	service.vba = vega
	service.appSetting = &common.AppSetting{ServerSetting: common.ServerSetting{CypherExecutor: common.CypherExecutorBindJoin}}
	return service
}

func relationServiceData() map[string][]map[string]any {
	return map[string][]map[string]any{
		"res_order": {
			{"f_id": "o-1", "f_cust_code": "c-1", "f_total": 5},
			{"f_id": "o-2", "f_cust_code": "c-9", "f_total": 7},
		},
		"res_customer": {{"f_code": "c-1", "f_name": "Ann"}},
	}
}

func TestQueryRunsThroughTheBindJoinExecutorWhenConfigured(t *testing.T) {
	spans := recordSpans(t)
	vega := &accountDataAPI{api: newFakeDataAPI(relationServiceData())}
	service := bindJoinService(vega, nil)

	result, err := service.Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: interfaces.MAIN_BRANCH,
		Query: "MATCH (o:Order)-[:PLACED_BY]->(c:Customer) RETURN o.id AS id, c.name AS name",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	want := []map[string]any{{"id": "o-1", "name": "Ann"}}
	if !reflect.DeepEqual(result.Entries, want) {
		t.Fatalf("entries = %v, want %v", result.Entries, want)
	}
	if len(result.Columns) != 2 || result.Columns[0].Name != "id" || result.Columns[1].Name != "name" {
		t.Fatalf("columns = %+v", result.Columns)
	}
	if len(result.TraceDescriptor) == 0 {
		t.Fatal("the trace descriptor is the same whichever executor runs")
	}
	if spans.attrs["cypher.executor"] != "bindjoin" || spans.attrs["cypher.execution_path"] != "caller" {
		t.Fatalf("span attrs = %v", spans.attrs)
	}
}

func TestBindJoinRetriesRefusedCallerUnderProxyAccount(t *testing.T) {
	spans := recordSpans(t)
	caller, _ := callerContext().Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	vega := &accountDataAPI{api: newFakeDataAPI(relationServiceData()), refused: map[string]bool{caller.ID: true}}
	service := bindJoinService(vega, &stubProxies{mapping: readyProxy()})

	result, err := service.Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: interfaces.MAIN_BRANCH,
		Query: "MATCH (o:Order)-[:PLACED_BY]->(c:Customer) RETURN o.id AS id, c.name AS name",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(result.Entries) != 1 {
		t.Fatalf("entries = %v", result.Entries)
	}
	if vega.readers[0] != caller.ID || vega.readers[len(vega.readers)-1] != "proxy-1" {
		t.Fatalf("readers = %v, want the caller refused and then the proxy", vega.readers)
	}
	if spans.attrs["cypher.execution_path"] != "proxy" {
		t.Fatalf("span attrs = %v", spans.attrs)
	}
}

func TestBindJoinReportsRefusalWhenProxyUnavailable(t *testing.T) {
	withLocale(t)
	caller, _ := callerContext().Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	vega := &accountDataAPI{api: newFakeDataAPI(relationServiceData()), refused: map[string]bool{caller.ID: true}}
	service := bindJoinService(vega, nil)

	_, err := service.Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: interfaces.MAIN_BRANCH,
		Query: "MATCH (o:Order) RETURN o.id",
	})
	if statusOf(t, err) != http.StatusForbidden {
		t.Fatalf("err = %v, want forbidden", err)
	}
}

func TestBindJoinFallsBackToSQLPastTheReadBudget(t *testing.T) {
	spans := recordSpans(t)
	rows := make([]map[string]any, defaultBindJoinLimits.maxRows+1)
	for i := range rows {
		rows[i] = map[string]any{"f_id": strconv.Itoa(i)}
	}
	vega := &accountDataAPI{api: newFakeDataAPI(map[string][]map[string]any{"res_order": rows}),
		sqlResponse: &interfaces.RawQueryResponse{
			Columns: []interfaces.RawQueryColumn{{Name: "o.id", Type: "string"}},
			Entries: []map[string]any{{"o.id": "from-sql"}},
		}}
	service := bindJoinService(vega, nil)

	result, err := service.Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: interfaces.MAIN_BRANCH,
		Query: "MATCH (o:Order) RETURN count(*) AS n",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if vega.sqlRequest == nil || !strings.HasPrefix(vega.sqlRequest.Query, "SELECT ") ||
		vega.sqlRequest.InputDialect != interfaces.VEGA_DIALECT_MYSQL {
		t.Fatalf("sql request = %+v, want the compiled MySQL statement", vega.sqlRequest)
	}
	if !reflect.DeepEqual(result.Entries, vega.sqlResponse.Entries) {
		t.Fatalf("entries = %v, want the SQL path's", result.Entries)
	}
	if spans.attrs["cypher.fallback"] != "sql_read_budget" {
		t.Fatalf("span attrs = %v", spans.attrs)
	}
}

// slowDataAPI holds every read until its context ends, the way a data source
// that cannot answer in time does.
type slowDataAPI struct {
	accountDataAPI
}

func (v *slowDataAPI) QueryResourceDataAs(ctx context.Context, _ interfaces.AccountInfo, _ string,
	_ *interfaces.ResourceDataQueryParams) (*interfaces.DatasetQueryResponse, error) {
	<-ctx.Done()
	// A read cut off partway reports a transport failure, not the deadline.
	return nil, interfaces.NewDependencyError("vega", "resource_data", interfaces.DependencyTransportKind(ctx.Err()), 0)
}

// #2203 review: the executor running out of its own time, while the caller
// still waits, falls back to SQL like the read budget does.
func TestBindJoinFallsBackToSQLWhenItRunsOutOfTime(t *testing.T) {
	spans := recordSpans(t)
	vega := &slowDataAPI{accountDataAPI{sqlResponse: &interfaces.RawQueryResponse{
		Entries: []map[string]any{{"o.id": "from-sql"}},
	}}}
	service := bindJoinService(vega, nil)
	service.bindJoinTimeout = 20 * time.Millisecond

	result, err := service.Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: interfaces.MAIN_BRANCH, Query: "MATCH (o:Order) RETURN o.id",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if vega.sqlRequest == nil || !reflect.DeepEqual(result.Entries, vega.sqlResponse.Entries) {
		t.Fatalf("entries = %v, sql = %+v; want the SQL path's answer", result.Entries, vega.sqlRequest)
	}
	if spans.attrs["cypher.fallback"] != "sql_timeout" {
		t.Fatalf("span attrs = %v", spans.attrs)
	}
}

// A caller that went away is not served by starting the query over as SQL.
func TestBindJoinDoesNotFallBackWhenTheCallerGoesAway(t *testing.T) {
	withLocale(t)
	vega := &slowDataAPI{}
	service := bindJoinService(vega, nil)
	ctx, cancel := context.WithTimeout(callerContext(), 20*time.Millisecond)
	defer cancel()

	_, err := service.Query(ctx, interfaces.CypherQuery{
		KNID: "kn_1", Branch: interfaces.MAIN_BRANCH, Query: "MATCH (o:Order) RETURN o.id",
	})
	if err == nil || vega.sqlRequest != nil {
		t.Fatalf("err = %v, sql = %+v; want a failure and no SQL attempt", err, vega.sqlRequest)
	}
}

// Only the read budget and the executor's own timeout fall back: any other
// failure is reported as it is.
func TestBindJoinDoesNotFallBackOnOtherFailures(t *testing.T) {
	withLocale(t)
	caller, _ := callerContext().Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	vega := &accountDataAPI{api: newFakeDataAPI(relationServiceData()), refused: map[string]bool{caller.ID: true}}
	service := bindJoinService(vega, nil)

	_, err := service.Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: interfaces.MAIN_BRANCH, Query: "MATCH (o:Order) RETURN o.id",
	})
	if statusOf(t, err) != http.StatusForbidden || vega.sqlRequest != nil {
		t.Fatalf("err = %v, sql = %+v; want forbidden and no SQL attempt", err, vega.sqlRequest)
	}
}

// The worldcup network on the validation VM stores scores as strings. SQL
// computes with them by converting, so the executor has to as well.
func TestBindJoinComputesWithNumericStringsLikeMySQL(t *testing.T) {
	data := bindJoinData()
	for _, row := range data["res_order"] {
		if amount, ok := row["f_amount"].(int); ok {
			row["f_amount"] = strconv.Itoa(amount)
		}
	}
	api := newFakeDataAPI(data)

	plan, schema := compileBindJoin(t, "MATCH (o:Order) RETURN sum(o.amount) AS s, max(o.amount) AS hi", nil)
	columns, entries, err := runBindJoin(context.Background(), plan, schema, api.read, defaultBindJoinLimits, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if got := entries[0]["s"]; got != json.Number("140.0") {
		t.Fatalf("sum = %v, want 140.0, a double as MySQL gives over strings", got)
	}
	// MAX compares the strings as strings, as it does over a text column.
	if got := entries[0]["hi"]; got != "70" {
		t.Fatalf("max = %v, want \"70\"", got)
	}
	if columns[0].Type != "float" || columns[1].Type != "string" {
		t.Fatalf("columns = %+v, want the types of the values returned", columns)
	}

	rows, err := runFake(t, newFakeDataAPI(data), defaultBindJoinLimits,
		"MATCH (o:Order) WHERE o.amount * 2 > 100 RETURN o.id", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rows, [][]any{{2}}) {
		t.Fatalf("rows = %v, want [[2]]", rows)
	}
}

func TestLeadingFloat(t *testing.T) {
	for text, want := range map[string]float64{
		"3": 3, " 12abc": 12, "1.5e2x": 150, "abc": 0, "-4": -4, "": 0, ".": 0, "1e": 1, "0.25": 0.25,
	} {
		if got := leadingFloat(text); got != want {
			t.Errorf("leadingFloat(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestBindJoinColumnsFallBackToTheModelWhenAllNull(t *testing.T) {
	plan, schema := compileBindJoin(t, "MATCH (o:Order) RETURN o.amount AS a, count(*) AS n", nil)
	columns := bindJoinColumns(plan, schema, []map[string]any{{"a": nil, "n": json.Number("1")}})
	if columns[0].Type != "integer" || columns[1].Type != "integer" {
		t.Fatalf("columns = %+v", columns)
	}
}

// #2203 review: CONTAINS is pushed as like, whose value vega-backend reads with
// backslash escapes and a bare % refused, so the executor has to escape both
// to keep the literal match the SQL path makes.
func TestBindJoinContainsMatchesLiterally(t *testing.T) {
	data := bindJoinData()
	data["res_customer"] = append(data["res_customer"],
		map[string]any{"f_code": "c3", "f_region": "north", "f_name": "50% off"},
		map[string]any{"f_code": "c4", "f_region": "north", "f_name": `a\_b`},
		map[string]any{"f_code": "c5", "f_region": "north", "f_name": "a_b"},
	)
	for query, want := range map[string][][]any{
		"MATCH (c:Customer) WHERE c.name CONTAINS '50%' RETURN c.code":   {{"c3"}},
		`MATCH (c:Customer) WHERE c.name CONTAINS 'a\\_b' RETURN c.code`: {{"c4"}},
		"MATCH (c:Customer) WHERE c.name CONTAINS 'a_b' RETURN c.code":   {{"c5"}},
	} {
		rows, err := runFake(t, newFakeDataAPI(data), defaultBindJoinLimits, query, nil)
		if err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		if !reflect.DeepEqual(rows, want) {
			t.Fatalf("%s: rows = %v, want %v", query, rows, want)
		}
	}
	pushed, _ := pushFilter(PlanStringMatch{Column: "f", Operator: Contains, Value: `50%\x`})
	if got := pushed.condition["value"]; got != `50\%\\x` {
		t.Fatalf("like value = %q", got)
	}
}

// #2203 review: vega-backend accepts at most 100 operands in one and/or.
func TestPushFilterNestsLargeJunctions(t *testing.T) {
	operands := make([]PlanPredicate, 250)
	for i := range operands {
		operands[i] = PlanCondition{Column: "f", Operator: "=", Value: Literal{Kind: LiteralInteger, Integer: int64(i)}}
	}
	pushed, ok := pushFilter(PlanLogical{Operator: "OR", Operands: operands})
	if !ok {
		t.Fatal("not pushed")
	}
	var leaves int
	var walk func(condition map[string]any)
	walk = func(condition map[string]any) {
		subs, ok := condition["sub_conditions"].([]map[string]any)
		if !ok {
			leaves++
			return
		}
		if len(subs) > maxSubConditions || condition["operation"] != "or" {
			t.Fatalf("junction of %d %v operands", len(subs), condition["operation"])
		}
		for _, sub := range subs {
			walk(sub)
		}
	}
	walk(pushed.condition)
	if leaves != 250 {
		t.Fatalf("%d leaves, want 250", leaves)
	}
}

// #2203 review: a date property is compared by wall-clock time in the
// executor, never pushed, since vega-backend only takes epoch milliseconds or
// RFC 3339 for one and converts them in its session's zone.
func TestBindJoinComparesDatesByWallClock(t *testing.T) {
	rows, api := mustRunFake(t, "MATCH (o:Order) WHERE o.placed_at >= '2024-01-01' AND o.placed_at < '2024-02-01' "+
		"AND o.region IN ['east', 'west'] RETURN o.id ORDER BY o.id", nil)
	if !reflect.DeepEqual(rows, [][]any{{1}, {2}}) {
		t.Fatalf("rows = %v", rows)
	}
	read := api.readsOf("res_order")[0].FilterCondition
	if fmt.Sprint(read) != fmt.Sprint(fieldCondition("f_region", "in", []any{"east", "west"})) {
		t.Fatalf("pushed %v, want only the region condition", read)
	}
	rows, _ = mustRunFake(t, "MATCH (o:Order) WHERE o.placed_at = '2024-01-01 00:00:00' OR o.placed_at IN ['2024-02-01'] "+
		"RETURN o.id ORDER BY o.id", nil)
	if !reflect.DeepEqual(rows, [][]any{{1}, {3}}) {
		t.Fatalf("rows = %v", rows)
	}
}

// #2203 review: a key stored as a string on one side and a number on the other
// joins as SQL joins it, by converting the string; two strings still compare
// exactly.
func TestBindJoinJoinsStringKeysToNumberKeys(t *testing.T) {
	data := bindJoinData()
	data["res_order"] = []map[string]any{
		{"f_id": 1, "f_cust": "42", "f_region": "east"},
		{"f_id": 2, "f_cust": "042", "f_region": "east"},
		{"f_id": 3, "f_cust": "43", "f_region": "east"},
	}
	data["res_customer"] = []map[string]any{{"f_code": 42, "f_region": "east", "f_name": "Num"}}
	rows, err := runFake(t, newFakeDataAPI(data), defaultBindJoinLimits,
		"MATCH (o:Order)-[:PLACED_BY]->(c:Customer) RETURN o.id, c.name ORDER BY o.id", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rows, [][]any{{1, "Num"}, {2, "Num"}}) {
		t.Fatalf("rows = %v", rows)
	}

	data["res_customer"] = []map[string]any{{"f_code": "42", "f_region": "east", "f_name": "Str"}}
	rows, err = runFake(t, newFakeDataAPI(data), defaultBindJoinLimits,
		"MATCH (o:Order)-[:PLACED_BY]->(c:Customer) RETURN o.id, c.name ORDER BY o.id", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rows, [][]any{{1, "Str"}}) {
		t.Fatalf("rows = %v, want only the exact string match", rows)
	}
}

// #2203 review: once nothing matches, no further table is read.
func TestBindJoinStopsReadingOnceNothingMatches(t *testing.T) {
	_, api := mustRunFake(t, "MATCH (i:Item)-[:BELONGS_TO]->(o:Order) WHERE o.region = 'north' RETURN i.id", nil)
	if items := api.readsOf("res_item"); len(items) != 0 {
		t.Fatalf("items read %d times after the orders matched nothing", len(items))
	}
}

// #2203 review: vega-backend pages a table with an offset, so a table without
// a primary key is sorted by every column read.
func TestBindJoinSortsKeylessTablesByEveryColumn(t *testing.T) {
	// DISTINCT keeps the read a full cursor read rather than one sorted page.
	plan, schema := compileBindJoin(t, "MATCH (c:Customer) RETURN DISTINCT c.name", nil)
	schema.objectTypesByID["ot_customer"].PrimaryKeys = nil
	api := newFakeDataAPI(bindJoinData())
	if _, _, err := runBindJoin(context.Background(), plan, schema, api.read, defaultBindJoinLimits, 1000); err != nil {
		t.Fatal(err)
	}
	var sorted []string
	for _, field := range api.readsOf("res_customer")[0].Sort {
		sorted = append(sorted, field.Field)
	}
	if !reflect.DeepEqual(sorted, []string{"f_name"}) {
		t.Fatalf("sorted by %v, want every column read", sorted)
	}
}

// #2203 review: SQL compares a string with a number as doubles on both sides,
// so an id past 2^53 stored as a string still joins the same id stored as a
// number.
func TestBindJoinJoinsLargeStringIDsToNumberIDs(t *testing.T) {
	data := bindJoinData()
	data["res_order"] = []map[string]any{{"f_id": 1, "f_cust": "1234567890123456789", "f_region": "east"}}
	data["res_customer"] = []map[string]any{
		{"f_code": json.Number("1234567890123456789"), "f_region": "east", "f_name": "Snowflake"},
	}
	rows, err := runFake(t, newFakeDataAPI(data), defaultBindJoinLimits,
		"MATCH (o:Order)-[:PLACED_BY]->(c:Customer) RETURN o.id, c.name", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rows, [][]any{{1, "Snowflake"}}) {
		t.Fatalf("rows = %v, want the large ids joined", rows)
	}
}

// A query over one table hands ORDER BY, SKIP and LIMIT to the data source and
// reads one page, instead of reading the whole table to keep a few rows.
func TestBindJoinReadsOneSortedPageForASingleTable(t *testing.T) {
	rows, api := mustRunFake(t, "MATCH (o:Order) WHERE o.amount IS NOT NULL RETURN o.id AS id, o.amount AS a "+
		"ORDER BY a DESC SKIP 1 LIMIT 1", nil)
	if !reflect.DeepEqual(rows, [][]any{{1, 50}}) {
		t.Fatalf("rows = %v", rows)
	}
	reads := api.readsOf("res_order")
	if len(reads) != 1 || reads[0].Paging.Mode != interfaces.VEGA_PAGING_MODE_SINGLE || reads[0].Paging.Limit != 2 {
		t.Fatalf("reads = %+v, want one single page of skip+limit rows", reads)
	}
	var sorted []string
	for _, field := range reads[0].Sort {
		sorted = append(sorted, field.Field+" "+field.Direction)
	}
	if !reflect.DeepEqual(sorted, []string{"f_amount desc", "f_id asc"}) {
		t.Fatalf("sort = %v, want the query's key then the primary key", sorted)
	}

	// The default LIMIT bounds the page when the query wrote none.
	_, api = mustRunFake(t, "MATCH (o:Order) RETURN o.id", nil)
	if reads := api.readsOf("res_order"); reads[0].Paging.Limit != interfaces.CYPHER_DEFAULT_LIMIT {
		t.Fatalf("page = %d, want the default limit", reads[0].Paging.Limit)
	}
}

func TestBindJoinReadsEverythingWhenTheDataSourceCannotFinishTheQuery(t *testing.T) {
	for _, query := range []string{
		"MATCH (i:Item)-[:BELONGS_TO]->(o:Order) RETURN i.id LIMIT 1",
		"MATCH (o:Order) RETURN count(*) AS n",
		"MATCH (o:Order) RETURN DISTINCT o.region LIMIT 1",
		"MATCH (c:Customer) WHERE c.name ENDS WITH 'e' RETURN c.name LIMIT 1",
		"MATCH (o:Order) RETURN o.id, o.amount * 2 AS d ORDER BY d LIMIT 1",
		"MATCH (o:Order) WITH o.region AS r, count(*) AS n WHERE n >= 2 RETURN r LIMIT 1",
		"MATCH (o:Order) WITH o, count(*) AS n RETURN o.id LIMIT 1",
	} {
		_, api := mustRunFake(t, query, nil)
		for _, r := range api.reads {
			if r.params.Paging.Mode == interfaces.VEGA_PAGING_MODE_SINGLE {
				t.Fatalf("%s: read a single page, but the executor has to finish this query itself", query)
			}
		}
	}
}
