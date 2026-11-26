// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_target

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	cond "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common/condition"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	omock "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces/mock"
)

func licensed(t *testing.T, yes bool) {
	t.Helper()
	previous := EventActionsLicensed
	EventActionsLicensed = func() bool { return yes }
	t.Cleanup(func() { EventActionsLicensed = previous })
}

var shanghai = func() *time.Location {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		panic(err)
	}
	return location
}()

// 2026-11-24 01:30 UTC is 09:30 in Shanghai.
var checkAt = time.Date(2026, 11, 24, 1, 30, 0, 0, time.UTC)

func eventAction() *interfaces.ActionType {
	return &interfaces.ActionType{
		ATID: "po_overdue_recognize", ATName: "超期订单", ActionType: "add", ObjectTypeID: "purchase_order",
		OperationConfig: &interfaces.ActionOperationConfig{Type: interfaces.ActionOperationEvent, EventClass: "po_overdue"},
		Condition:       &cond.CondCfg{Name: "due_time", Operation: "<", ValueFrom: interfaces.ActionValueFromCheckTime, Value: "-PT48H"},
		Schedule:        interfaces.Schedule{Type: "CRON", Expression: "0 8 * * *", Timezone: "Asia/Shanghai"},
		RuleVersion:     "a1b2c3", ExecutionSubject: &interfaces.AccountInfo{ID: "u-1", Type: "user"},
	}
}

var purchaseOrder = interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
	OTID: "purchase_order", OTName: "采购订单", PrimaryKeys: []string{"po_id"}, DisplayKey: "po_name",
	DataProperties: []cond.DataProperty{
		{Name: "po_id", Type: "integer"}, {Name: "po_name", Type: "string"},
		{Name: "due_time", Type: "datetime"}, {Name: "ship_time", Type: "datetime"}, {Name: "status", Type: "string"},
	},
}}

func TestEventDefinitionIsCheckedWithoutTheNetwork(t *testing.T) {
	licensed(t, true)
	if err := ValidateDefinition(eventAction()); err != nil {
		t.Fatalf("a condition-based recognition action is valid: %v", err)
	}
	for name, edit := range map[string]func(*interfaces.ActionType){
		"no subject":     func(a *interfaces.ActionType) { a.ObjectTypeID = "" },
		"no event class": func(a *interfaces.ActionType) { a.OperationConfig.EventClass = "" },
		"an action source": func(a *interfaces.ActionType) {
			a.ActionSource = interfaces.ActionSource{Type: "tool", BoxID: "b", ToolID: "t"}
		},
		"parameters": func(a *interfaces.ActionType) {
			a.Parameters = []interfaces.Parameter{{Name: "x", ValueFrom: "const", Value: 1}}
		},
		"no time zone":    func(a *interfaces.ActionType) { a.Schedule.Timezone = "" },
		"a bad time zone": func(a *interfaces.ActionType) { a.Schedule.Timezone = "Mars/Olympus" },
		"no schedule":     func(a *interfaces.ActionType) { a.Schedule = interfaces.Schedule{} },
		"neither basis":   func(a *interfaces.ActionType) { a.Condition = nil },
		"both bases": func(a *interfaces.ActionType) {
			a.OperationConfig.Targets = &interfaces.ActionEventTargets{Cypher: &interfaces.ActionCypherTargets{Query: "MATCH (o) RETURN o"}}
		},
		"an input condition": func(a *interfaces.ActionType) { a.Condition.ValueFrom = "input" },
		"a bad duration":     func(a *interfaces.ActionType) { a.Condition.Value = "48 hours" },
		"another object type": func(a *interfaces.ActionType) {
			a.Condition.ObjectTypeID = "supplier"
		},
		"two target sources": func(a *interfaces.ActionType) {
			a.Condition = nil
			a.OperationConfig.Targets = &interfaces.ActionEventTargets{
				Cypher:   &interfaces.ActionCypherTargets{Query: "MATCH (o) RETURN o"},
				Function: &interfaces.ActionFunctionTargets{BoxID: "b", ToolID: "t"},
			}
		},
		"a parameter named check_time": func(a *interfaces.ActionType) {
			a.Condition = nil
			a.OperationConfig.Targets = &interfaces.ActionEventTargets{Cypher: &interfaces.ActionCypherTargets{
				Query: "MATCH (o) RETURN o", Parameters: map[string]interfaces.ActionTargetParameter{"check_time": {ValueFrom: "const", Value: "x"}},
			}}
		},
		"a bad operator": func(a *interfaces.ActionType) {
			a.Condition = nil
			a.OperationConfig.Targets = &interfaces.ActionEventTargets{Metric: &interfaces.ActionMetricTargets{MetricID: "m", Version: 1, Operator: "~"}}
		},
		"event fields on an object action": func(a *interfaces.ActionType) {
			a.OperationConfig.Type = "object"
		},
	} {
		t.Run(name, func(t *testing.T) {
			action := eventAction()
			edit(action)
			if err := ValidateDefinition(action); err == nil {
				t.Fatalf("expected the definition to be refused")
			}
		})
	}
}

func TestEventActionsNeedTheProfessionalEdition(t *testing.T) {
	licensed(t, false)
	err := ValidateDefinition(eventAction())
	if err == nil || err.Error() != `unsupported action operation "event"` {
		t.Fatalf("an unlicensed record-event action reads as an unknown operation, got %v", err)
	}
}

func TestEventActionAcceptsNoTargets(t *testing.T) {
	licensed(t, true)
	resolver := &Resolver{}
	instances, data, err := resolver.Resolve(context.Background(), eventAction(), &interfaces.ActionExecutionRequest{KNID: "kn"})
	if err != nil || instances != nil || data != nil {
		t.Fatalf("no targets resolve to nothing: %v %v %v", instances, data, err)
	}
	_, _, err = resolver.Resolve(context.Background(), eventAction(), &interfaces.ActionExecutionRequest{
		KNID: "kn", InstanceIdentities: []map[string]any{{"po_id": 1}},
	})
	if err == nil {
		t.Fatalf("explicit targets are refused")
	}
	schema, err := resolver.ExecutionSchema(context.Background(), eventAction(), "kn", "main")
	if err != nil {
		t.Fatal(err)
	}
	identities := schema["properties"].(map[string]any)["_instance_identities"].(map[string]any)
	if identities["maxItems"] != 0 {
		t.Fatalf("the schema allows no targets: %v", schema)
	}
}

func TestCheckClockValues(t *testing.T) {
	clock := CheckClock{At: checkAt, Location: shanghai}
	for _, test := range []struct {
		from  string
		value any
		want  string
	}{
		{interfaces.ActionValueFromCheckTime, nil, "2026-11-24T09:30:00+08:00"},
		{interfaces.ActionValueFromCheckTime, "-PT48H", "2026-11-22T09:30:00+08:00"},
		{interfaces.ActionValueFromCheckTime, "-P1M", "2026-10-24T09:30:00+08:00"},
		{interfaces.ActionValueFromCheckTime, "PT1H30M", "2026-11-24T11:00:00+08:00"},
		{interfaces.ActionValueFromCheckDate, nil, "2026-11-24"},
		{interfaces.ActionValueFromCheckDate, "-P3D", "2026-11-21"},
		{interfaces.ActionValueFromCheckDate, "-P1W", "2026-11-17"},
	} {
		got, err := clock.Value(test.from, test.value)
		if err != nil || got != test.want {
			t.Errorf("%s %v = %v (%v), want %s", test.from, test.value, got, err, test.want)
		}
	}
	// The check day is the schedule zone's: 20:00 UTC is already the next day in Shanghai.
	late := CheckClock{At: time.Date(2026, 11, 24, 20, 0, 0, 0, time.UTC), Location: shanghai}
	if got, _ := late.Value(interfaces.ActionValueFromCheckDate, nil); got != "2026-11-25" {
		t.Errorf("check day = %v", got)
	}
	for _, bad := range []any{"P", "PT", "-P1H", 48, "48h"} {
		if _, err := clock.Value(interfaces.ActionValueFromCheckTime, bad); err == nil {
			t.Errorf("%v is not a duration", bad)
		}
	}
	if got, _ := clock.Value("const", "x"); got != "x" {
		t.Errorf("a constant stays itself")
	}
}

func TestTargetIdentityKey(t *testing.T) {
	for _, test := range []struct {
		identity map[string]any
		want     string
	}{
		{map[string]any{"id": json.Number("9007199254740993")}, "9007199254740993"},
		{map[string]any{"id": "PO-1"}, "PO-1"},
		{map[string]any{"id": float64(12)}, "12"},
		{map[string]any{"line": json.Number("2"), "order": "PO-1"}, `{"line":2,"order":"PO-1"}`},
		{map[string]any{}, ""},
	} {
		if got := interfaces.TargetIdentityKey(test.identity); got != test.want {
			t.Errorf("%v = %q, want %q", test.identity, got, test.want)
		}
	}
}

type selectorMocks struct {
	models  *omock.MockOntologyManagerAccess
	objects *omock.MockObjectTypeService
	queries *omock.MockQueryAuthorizationService
	rules   *omock.MockEventRuleAccess
	tools   *omock.MockAgentOperatorAccess
	metrics *omock.MockObjectMetricQueryServiceV1
}

func newSelector(t *testing.T, limit int) (*Selector, selectorMocks) {
	t.Helper()
	licensed(t, true)
	ctrl := gomock.NewController(t)
	m := selectorMocks{
		models: omock.NewMockOntologyManagerAccess(ctrl), objects: omock.NewMockObjectTypeService(ctrl),
		queries: omock.NewMockQueryAuthorizationService(ctrl), rules: omock.NewMockEventRuleAccess(ctrl),
		tools: omock.NewMockAgentOperatorAccess(ctrl), metrics: omock.NewMockObjectMetricQueryServiceV1(ctrl),
	}
	m.models.EXPECT().GetObjectType(gomock.Any(), "kn", "main", "purchase_order").Return(purchaseOrder, true, nil).AnyTimes()
	m.queries.EXPECT().AuthorizeObjectTypeQuery(gomock.Any(), "kn", "main", "purchase_order").Return(nil).AnyTimes()
	return &Selector{Models: m.models, Objects: m.objects, Queries: m.queries, Rules: m.rules, Tools: m.tools,
		Metrics: m.metrics, Limit: limit}, m
}

func order(id int64, name string) map[string]any {
	return map[string]any{
		"po_id": json.Number(jsonInt(id)), "po_name": name, "due_time": "2026-11-20T00:00:00+08:00",
		interfaces.SYSTEM_PROPERTY_INSTANCE_IDENTITY: map[string]any{"po_id": json.Number(jsonInt(id))},
		interfaces.SYSTEM_PROPERTY_DISPLAY:           name,
	}
}

func jsonInt(id int64) string { return strconv.FormatInt(id, 10) }

func TestConditionSelectsThroughTheObjectQuery(t *testing.T) {
	selector, m := newSelector(t, 100)
	action := eventAction()
	action.Condition = &cond.CondCfg{Operation: "and", SubConds: []*cond.CondCfg{
		{Name: "due_time", Operation: "<", ValueFrom: interfaces.ActionValueFromCheckTime, Value: "-PT48H"},
		{Name: "ship_time", Operation: ">", ValueFrom: "property", Value: "due_time"},
		{Name: "status", Operation: "==", ValueFrom: "const", Value: "待发货"},
	}}
	var query *interfaces.ObjectQueryBaseOnObjectType
	m.objects.EXPECT().ScanObjectsByObjectTypeID(gomock.Any(), gomock.Any(), 100).DoAndReturn(
		func(_ context.Context, q *interfaces.ObjectQueryBaseOnObjectType, _ int) (interfaces.Objects, error) {
			query = q
			return interfaces.Objects{Datas: []map[string]any{order(1, "PO 1"), order(2, "PO 2"), order(1, "PO 1 again")}, RowFilterApplied: true}, nil
		})
	selection, err := selector.Select(context.Background(), "kn", "main", action, CheckClock{At: checkAt, Location: shanghai})
	if err != nil {
		t.Fatal(err)
	}
	subs := query.ActualCondition.SubConds
	if subs[0].ValueFrom != "const" || subs[0].Value != "2026-11-22T09:30:00+08:00" {
		t.Errorf("the check time becomes a constant: %+v", subs[0])
	}
	if subs[1].ValueFrom != cond.ValueFrom_Field || subs[1].Value != "due_time" {
		t.Errorf("a property reference becomes a field comparison: %+v", subs[1])
	}
	if action.Condition.SubConds[0].ValueFrom != interfaces.ActionValueFromCheckTime {
		t.Errorf("the stored condition is left as it was")
	}
	if query.KNID != "kn" || query.Branch != "main" || query.ObjectTypeID != "purchase_order" ||
		!reflect.DeepEqual(query.RequiredFullProperties, []string{"po_id", "due_time", "ship_time", "status"}) {
		t.Errorf("query = %+v", query)
	}
	if len(selection.Targets) != 2 || selection.Targets[0].Key != "1" || selection.Targets[0].Name != "PO 1" ||
		selection.Targets[1].Key != "2" {
		t.Fatalf("targets = %+v", selection.Targets)
	}
	if selection.Targets[0].Attributes["due_time"].Label != "2026-11-20T00:00:00+08:00" {
		t.Errorf("the condition's properties become attributes: %+v", selection.Targets[0].Attributes)
	}
	if selection.Covered != nil || selection.Limited {
		t.Errorf("a complete read covers every object")
	}
	if selection.RowFilterApplied == nil || !*selection.RowFilterApplied || !strings.Contains(selection.Message, "row filter") {
		t.Errorf("the row filter is reported: %+v", selection)
	}
}

func TestATruncatedSelectionCoversOnlyItsHits(t *testing.T) {
	selector, m := newSelector(t, 2)
	m.objects.EXPECT().ScanObjectsByObjectTypeID(gomock.Any(), gomock.Any(), 2).Return(interfaces.Objects{
		Datas: []map[string]any{order(1, "a"), order(2, "b"), order(3, "c")},
	}, nil)
	selection, err := selector.Select(context.Background(), "kn", "main", eventAction(), CheckClock{At: checkAt, Location: shanghai})
	if err != nil {
		t.Fatal(err)
	}
	if !selection.Limited || len(selection.Targets) != 2 || !reflect.DeepEqual(selection.Covered, []string{"1", "2"}) {
		t.Fatalf("selection = %+v", selection)
	}
}

func TestAConditionSelectionWithoutIdentityFails(t *testing.T) {
	selector, m := newSelector(t, 10)
	m.objects.EXPECT().ScanObjectsByObjectTypeID(gomock.Any(), gomock.Any(), 10).Return(interfaces.Objects{
		Datas: []map[string]any{{"po_name": "masked"}},
	}, nil)
	_, err := selector.Select(context.Background(), "kn", "main", eventAction(), CheckClock{At: checkAt, Location: shanghai})
	var refused *SelectionError
	if !errors.As(err, &refused) {
		t.Fatalf("an object without identity fails the selection, got %v", err)
	}
}

func cypherAction(parameters map[string]interfaces.ActionTargetParameter) *interfaces.ActionType {
	action := eventAction()
	action.Condition = nil
	action.OperationConfig.Targets = &interfaces.ActionEventTargets{Cypher: &interfaces.ActionCypherTargets{
		Query: "MATCH (o:purchase_order) WHERE o.due_time < $since RETURN o", Parameters: parameters,
	}}
	return action
}

func TestCypherNodesAreTheTargets(t *testing.T) {
	selector, m := newSelector(t, 100)
	var sent *interfaces.CypherQueryRequest
	m.rules.EXPECT().QueryCypher(gomock.Any(), "kn", "main", gomock.Any()).DoAndReturn(
		func(_ context.Context, _, _ string, query *interfaces.CypherQueryRequest) (*interfaces.CypherQueryResult, error) {
			sent = query
			node := func(id string) map[string]any {
				return map[string]any{"kind": "object", "object_type_id": "purchase_order",
					"key": map[string]any{"po_id": map[string]any{"type": "integer", "value": id}}}
			}
			return &interfaces.CypherQueryResult{
				Columns:           []interfaces.CypherColumn{{Name: "o"}, {Name: "days"}},
				Entries:           []map[string]any{{"o": node("7"), "days": json.Number("3")}, {"o": node("8"), "days": json.Number("5")}},
				ValueTypes:        map[string]*interfaces.CypherValueType{"o": {Kind: "object", ObjectTypeID: "purchase_order"}},
				PermissionImpacts: []interfaces.CypherPermissionImpact{{ObjectTypeID: "purchase_order"}},
			}, nil
		})
	// The nodes carry no name: the display property is looked up.
	m.objects.EXPECT().ScanObjectsByObjectTypeID(gomock.Any(), gomock.Any(), 2).DoAndReturn(
		func(_ context.Context, q *interfaces.ObjectQueryBaseOnObjectType, _ int) (interfaces.Objects, error) {
			if q.ActualCondition.Operation != cond.OperationIn || !reflect.DeepEqual(q.ActualCondition.Value, []any{json.Number("7"), json.Number("8")}) {
				t.Errorf("names are read by in on the primary key: %+v", q.ActualCondition)
			}
			return interfaces.Objects{Datas: []map[string]any{order(7, "PO 7")}}, nil
		})
	selection, err := selector.Select(context.Background(), "kn", "main", cypherAction(map[string]interfaces.ActionTargetParameter{
		"since":  {ValueFrom: interfaces.ActionValueFromCheckTime, Value: "-P30D"},
		"status": {ValueFrom: "const", Value: "待发货"},
	}), CheckClock{At: checkAt, Location: shanghai})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"since": "2026-10-25T09:30:00+08:00", "status": "待发货",
		"check_time": "2026-11-24T09:30:00+08:00", "check_date": "2026-11-24"}
	if !reflect.DeepEqual(sent.Parameters, want) {
		t.Errorf("parameters = %v", sent.Parameters)
	}
	if len(selection.Targets) != 2 || selection.Targets[0].Key != "7" || selection.Targets[0].Name != "PO 7" ||
		selection.Targets[1].Name != "8" || selection.Targets[0].Attributes["days"].Key != "3" {
		t.Fatalf("targets = %+v", selection.Targets)
	}
	if !reflect.DeepEqual(selection.Targets[0].Identity, map[string]any{"po_id": json.Number("7")}) {
		t.Errorf("identity = %#v", selection.Targets[0].Identity)
	}
	if selection.RowFilterApplied == nil || *selection.RowFilterApplied {
		t.Errorf("a Cypher result says whether a row filter applied")
	}
}

func TestCypherTablesNeedAKeyColumn(t *testing.T) {
	selector, m := newSelector(t, 100)
	m.rules.EXPECT().QueryCypher(gomock.Any(), "kn", "main", gomock.Any()).Return(&interfaces.CypherQueryResult{
		Columns: []interfaces.CypherColumn{{Name: "key"}, {Name: "name"}},
		Entries: []map[string]any{{"key": json.Number("9007199254740993"), "name": "大单"}, {"key": nil}},
		Limited: true,
	}, nil)
	selection, err := selector.Select(context.Background(), "kn", "main", cypherAction(nil), CheckClock{At: checkAt, Location: shanghai})
	if err != nil {
		t.Fatal(err)
	}
	if len(selection.Targets) != 1 || selection.Targets[0].Key != "9007199254740993" || selection.Targets[0].Name != "大单" ||
		!selection.Limited || !reflect.DeepEqual(selection.Covered, []string{"9007199254740993"}) {
		t.Fatalf("selection = %+v", selection)
	}

	m.rules.EXPECT().QueryCypher(gomock.Any(), "kn", "main", gomock.Any()).Return(&interfaces.CypherQueryResult{
		Columns: []interfaces.CypherColumn{{Name: "po"}}, Entries: []map[string]any{{"po": "1"}},
	}, nil)
	_, err = selector.Select(context.Background(), "kn", "main", cypherAction(nil), CheckClock{At: checkAt, Location: shanghai})
	var refused *SelectionError
	if !errors.As(err, &refused) || !strings.Contains(refused.Reason, "key column") {
		t.Fatalf("a result of neither shape says which shape it needs: %v", err)
	}

	m.rules.EXPECT().QueryCypher(gomock.Any(), "kn", "main", gomock.Any()).Return(nil, errors.New("syntax error"))
	_, err = selector.Select(context.Background(), "kn", "main", cypherAction(nil), CheckClock{At: checkAt, Location: shanghai})
	if !errors.As(err, &refused) || !strings.Contains(refused.Reason, "syntax error") {
		t.Fatalf("a failed query fails the selection with its reason: %v", err)
	}
}

func TestFunctionTablesAreTheTargets(t *testing.T) {
	selector, m := newSelector(t, 100)
	action := eventAction()
	action.Condition = nil
	action.OperationConfig.Targets = &interfaces.ActionEventTargets{Function: &interfaces.ActionFunctionTargets{
		BoxID: "box", ToolID: "late_orders",
		Parameters: map[string]interfaces.ActionTargetParameter{"day": {ValueFrom: interfaces.ActionValueFromCheckDate, Value: "-P1D"}},
	}}
	m.tools.EXPECT().ExecuteTool(gomock.Any(), "box", "late_orders", gomock.Any()).DoAndReturn(
		func(_ context.Context, _, _ string, request interfaces.ToolExecutionRequest) (any, error) {
			if request.Body["day"] != "2026-11-23" {
				t.Errorf("arguments = %v", request.Body)
			}
			return map[string]any{
				"columns": []any{map[string]any{"name": "key"}, "name", "supplier"},
				"entries": []any{
					map[string]any{"key": "PO-1", "name": "一号", "supplier": "ACME"},
					map[string]any{"key": "PO-1", "name": "重复"},
					map[string]any{"key": strings.Repeat("k", 251)},
				},
			}, nil
		})
	selection, err := selector.Select(context.Background(), "kn", "main", action, CheckClock{At: checkAt, Location: shanghai})
	if err != nil {
		t.Fatal(err)
	}
	if len(selection.Targets) != 1 || selection.Targets[0].Name != "一号" || selection.Targets[0].Attributes["supplier"].Key != "ACME" {
		t.Fatalf("the first row of a key wins, and an overlong key is left out: %+v", selection.Targets)
	}
	if !strings.Contains(selection.Message, "longer than 250") {
		t.Errorf("message = %q", selection.Message)
	}
}

func metricAction(targets interfaces.ActionMetricTargets) *interfaces.ActionType {
	action := eventAction()
	action.Condition = nil
	action.OperationConfig.Targets = &interfaces.ActionEventTargets{Metric: &targets}
	return action
}

func metricContext(grain interfaces.ObjectMetricStatisticalGrainV1) *interfaces.ObjectMetricExecutionContextV1 {
	return &interfaces.ObjectMetricExecutionContextV1{Root: &interfaces.ObjectMetricRecordV1{Definition: interfaces.ObjectMetricDefinitionV1{
		ID: "on_time_rate", Version: 2, Name: "准时率",
		ResultSemantics: interfaces.ObjectMetricResultSemantics{StatisticalGrain: grain},
	}}}
}

func TestMetricGroupsBelowTheThresholdAreHits(t *testing.T) {
	selector, m := newSelector(t, 100)
	m.queries.EXPECT().AuthorizeObjectMetricQuery(gomock.Any(), "kn", "main", "on_time_rate").Return(nil)
	m.models.EXPECT().GetObjectMetricExecutionContext(gomock.Any(), "kn", "main", "on_time_rate", 2).Return(metricContext(
		interfaces.ObjectMetricStatisticalGrainV1{
			GroupByDimensions: []interfaces.ObjectMetricPropertyRefV1{{ObjectTypeID: "purchase_order", PropertyName: "po_id"}},
			TimeGranularity:   "day", Timezone: "Asia/Shanghai",
		}), nil)
	yesterday := time.Date(2026, 11, 23, 0, 0, 0, 0, shanghai).UnixMilli()
	group := func(id string, value any, status string) interfaces.ObjectMetricDataEntryV1 {
		return interfaces.ObjectMetricDataEntryV1{
			DimensionCells: []interfaces.ObjectMetricDimensionCellV1{{Ref: interfaces.ObjectMetricPropertyRefV1{ObjectTypeID: "purchase_order", PropertyName: "po_id"}, Value: id}},
			TimeBucket:     &interfaces.ObjectMetricQueryTimeRangeV1{Start: yesterday, End: yesterday + 24*3600*1000 - 1},
			Value:          value, Status: status,
		}
	}
	m.metrics.EXPECT().QueryObjectMetricDataV1(gomock.Any(), "kn", "main", "on_time_rate", 2, gomock.Any()).DoAndReturn(
		func(_ context.Context, _, _, _ string, _ int, request *interfaces.ObjectMetricQueryRequestV1) (interfaces.ObjectMetricDataV1, error) {
			// Yesterday in Shanghai: the last complete day before 09:30 on 11-24.
			start := time.Date(2026, 11, 23, 0, 0, 0, 0, shanghai).UnixMilli()
			if request.Time == nil || request.Time.Start != start || request.Time.End != start+24*3600*1000-1 ||
				request.RuntimeAnchor == nil || *request.RuntimeAnchor != checkAt.UnixMilli() || request.Having != nil {
				t.Errorf("request = %+v", request)
			}
			return interfaces.ObjectMetricDataV1{Status: "partial", Entries: []interfaces.ObjectMetricDataEntryV1{
				group("1", json.Number("0.8"), "ok"), group("2", json.Number("0.95"), "ok"), group("3", nil, "no_data"),
			}, Model: interfaces.ObjectMetricResultModelV1{Value: interfaces.ObjectMetricValueSemanticsV1{
				Unit: &interfaces.ObjectMetricUnitSpecV1{Code: "percent", DisplaySymbol: "%"}}}}, nil
		})
	m.objects.EXPECT().ScanObjectsByObjectTypeID(gomock.Any(), gomock.Any(), 1).Return(interfaces.Objects{
		Datas: []map[string]any{order(1, "PO 1")}}, nil)
	selection, err := selector.Select(context.Background(), "kn", "main",
		metricAction(interfaces.ActionMetricTargets{MetricID: "on_time_rate", Version: 2, Operator: "<", Value: 0.9}),
		CheckClock{At: checkAt, Location: shanghai})
	if err != nil {
		t.Fatal(err)
	}
	if len(selection.Targets) != 1 || selection.Targets[0].Key != "1" || selection.Targets[0].Name != "PO 1" {
		t.Fatalf("targets = %+v", selection.Targets)
	}
	hit := selection.Targets[0]
	if hit.Attributes["threshold"].Label != "< 0.9" || hit.Attributes["unit"].Key != "%" || hit.Attributes["value"].Key != "0.8" ||
		hit.Attributes["period_start"].Key != "2026-11-23T00:00:00+08:00" {
		t.Errorf("attributes = %+v", hit.Attributes)
	}
	detail := hit.Detail["metric"].(map[string]any)
	if detail["metric_id"] != "on_time_rate" || detail["version"] != 2 || detail["threshold"] != 0.9 || detail["operator"] != "<" {
		t.Errorf("detail = %+v", hit.Detail)
	}
	// The group without a value is not judged: the check covers only the groups it judged.
	if !reflect.DeepEqual(selection.Covered, []string{"1", "2"}) || selection.Limited {
		t.Errorf("covered = %v", selection.Covered)
	}
	if selection.RowFilterApplied != nil {
		t.Errorf("a metric does not say whether a row filter applied")
	}
}

func TestMetricSubjectFollowsItsGrain(t *testing.T) {
	selector, m := newSelector(t, 100)
	grain := func(dimensions ...interfaces.ObjectMetricPropertyRefV1) interfaces.ObjectMetricStatisticalGrainV1 {
		return interfaces.ObjectMetricStatisticalGrainV1{GroupByDimensions: dimensions}
	}
	byEvent := interfaces.ObjectMetricPropertyRefV1{ObjectTypeID: "po_late", PropertyName: "object_key"}
	m.models.EXPECT().ListRelationTypes(gomock.Any(), "kn", "main", gomock.Any()).Return([]interfaces.RelationType{{
		RTID: "po_late_subject", SourceObjectTypeID: "po_late", TargetObjectTypeID: "purchase_order", Type: "direct",
		MappingRules: []any{map[string]any{"source_property": map[string]any{"name": "object_key"}, "target_property": map[string]any{"name": "po_id"}}},
	}}, nil)
	metric := &interfaces.ActionMetricTargets{MetricID: "late_count", Version: 1, Operator: ">=", Value: 3}
	if err := selector.checkMetricSubject(context.Background(), "kn", "main", metric, grain(byEvent), &purchaseOrder); err != nil {
		t.Errorf("a property mapped to the subject's key by one direct relation leads to it: %v", err)
	}
	if err := selector.checkMetricSubject(context.Background(), "kn", "main", metric, grain(), &purchaseOrder); err == nil {
		t.Errorf("an ungrouped metric needs subject_key")
	}
	if err := selector.checkMetricSubject(context.Background(), "kn", "main", metric, grain(byEvent, byEvent), &purchaseOrder); err == nil {
		t.Errorf("two dimensions are not supported")
	}
	m.models.EXPECT().ListRelationTypes(gomock.Any(), "kn", "main", gomock.Any()).Return(nil, nil)
	if err := selector.checkMetricSubject(context.Background(), "kn", "main", metric, grain(byEvent), &purchaseOrder); err == nil {
		t.Errorf("a dimension that leads nowhere is refused")
	}
}

func TestAMetricWithoutDataJudgesNothing(t *testing.T) {
	selector, m := newSelector(t, 100)
	m.queries.EXPECT().AuthorizeObjectMetricQuery(gomock.Any(), "kn", "main", "total").Return(nil)
	m.models.EXPECT().GetObjectMetricExecutionContext(gomock.Any(), "kn", "main", "total", 1).
		Return(metricContext(interfaces.ObjectMetricStatisticalGrainV1{}), nil)
	m.metrics.EXPECT().QueryObjectMetricDataV1(gomock.Any(), "kn", "main", "total", 1, gomock.Any()).
		Return(interfaces.ObjectMetricDataV1{Status: "no_data"}, nil)
	selection, err := selector.Select(context.Background(), "kn", "main",
		metricAction(interfaces.ActionMetricTargets{MetricID: "total", Version: 1, Operator: ">", Value: 1, SubjectKey: "hq"}),
		CheckClock{At: checkAt, Location: shanghai})
	if err != nil {
		t.Fatal(err)
	}
	if len(selection.Targets) != 0 || selection.Covered == nil || len(selection.Covered) != 0 {
		t.Fatalf("no data is no recovery: %+v", selection)
	}

	m.queries.EXPECT().AuthorizeObjectMetricQuery(gomock.Any(), "kn", "main", "total").Return(nil)
	m.models.EXPECT().GetObjectMetricExecutionContext(gomock.Any(), "kn", "main", "total", 1).
		Return(metricContext(interfaces.ObjectMetricStatisticalGrainV1{}), nil)
	m.metrics.EXPECT().QueryObjectMetricDataV1(gomock.Any(), "kn", "main", "total", 1, gomock.Any()).
		Return(interfaces.ObjectMetricDataV1{Status: "unavailable"}, nil)
	_, err = selector.Select(context.Background(), "kn", "main",
		metricAction(interfaces.ActionMetricTargets{MetricID: "total", Version: 1, Operator: ">", Value: 1, SubjectKey: "hq"}),
		CheckClock{At: checkAt, Location: shanghai})
	var refused *SelectionError
	if !errors.As(err, &refused) {
		t.Fatalf("an unavailable metric fails the selection: %v", err)
	}
}

func TestLastCompletePeriod(t *testing.T) {
	at := time.Date(2026, 11, 25, 10, 0, 0, 0, shanghai) // a Wednesday
	for granularity, want := range map[string][2]time.Time{
		"hour":    {time.Date(2026, 11, 25, 9, 0, 0, 0, shanghai), time.Date(2026, 11, 25, 10, 0, 0, 0, shanghai)},
		"day":     {time.Date(2026, 11, 24, 0, 0, 0, 0, shanghai), time.Date(2026, 11, 25, 0, 0, 0, 0, shanghai)},
		"week":    {time.Date(2026, 11, 16, 0, 0, 0, 0, shanghai), time.Date(2026, 11, 23, 0, 0, 0, 0, shanghai)},
		"month":   {time.Date(2026, 10, 1, 0, 0, 0, 0, shanghai), time.Date(2026, 11, 1, 0, 0, 0, 0, shanghai)},
		"quarter": {time.Date(2026, 7, 1, 0, 0, 0, 0, shanghai), time.Date(2026, 10, 1, 0, 0, 0, 0, shanghai)},
		"year":    {time.Date(2025, 1, 1, 0, 0, 0, 0, shanghai), time.Date(2026, 1, 1, 0, 0, 0, 0, shanghai)},
	} {
		period, err := lastCompletePeriod(at, interfaces.ObjectMetricStatisticalGrainV1{TimeGranularity: granularity, Timezone: "Asia/Shanghai"})
		if err != nil || period.Start != want[0].UnixMilli() || period.End != want[1].UnixMilli()-1 {
			t.Errorf("%s: %+v %v", granularity, period, err)
		}
	}
	period, _ := lastCompletePeriod(at, interfaces.ObjectMetricStatisticalGrainV1{TimeGranularity: "week", WeekStart: "sunday", Timezone: "Asia/Shanghai"})
	if period.Start != time.Date(2026, 11, 15, 0, 0, 0, 0, shanghai).UnixMilli() {
		t.Errorf("a week may start on Sunday: %+v", period)
	}
}

func TestHitsAreClippedToTheLedger(t *testing.T) {
	selector, _ := newSelector(t, 100)
	attributes := map[string]interfaces.EventAttribute{}
	for i := 0; i < 70; i++ {
		attributes[string(rune('A'+i/26))+string(rune('a'+i%26))] = interfaces.EventAttribute{Key: strings.Repeat("v", 300), Label: "l"}
	}
	selection := &Selection{Subject: &interfaces.ObjectType{}, Targets: []EventTarget{{EventHit: interfaces.EventHit{
		Key: "k", Name: strings.Repeat("名", 300), Attributes: attributes,
		Detail: map[string]any{"metric": strings.Repeat("x", interfaces.EventDetailMaxBytes)},
	}}}}
	selector.finish(context.Background(), "kn", "main", selection, 100)
	hit := selection.Targets[0]
	if len([]rune(hit.Name)) != interfaces.EventNameMax || len(hit.Attributes) != interfaces.EventAttributesMax || hit.Detail != nil {
		t.Fatalf("name %d, attributes %d, detail %v", len([]rune(hit.Name)), len(hit.Attributes), hit.Detail != nil)
	}
	for _, attribute := range hit.Attributes {
		if len(attribute.Key) != interfaces.EventAttributeKeyMax {
			t.Fatalf("attribute key %d", len(attribute.Key))
		}
	}
}
