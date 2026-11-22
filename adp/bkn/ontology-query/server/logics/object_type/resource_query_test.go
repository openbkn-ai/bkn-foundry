// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package object_type

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	cond "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common/condition"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
)

// resourceQueryVega implements the unified Resource contract without category metadata.
type resourceQueryVega struct {
	request  *interfaces.ResourceDataQueryParams
	calls    int
	response *interfaces.DatasetQueryResponse
	err      error
}

func (v *resourceQueryVega) GetResourceSchema(context.Context, string) (*interfaces.ResourceSchemaResponse, error) {
	return nil, fmt.Errorf("a data query must not read the Resource schema")
}

func (v *resourceQueryVega) QueryResourceData(_ context.Context, _ string, params *interfaces.ResourceDataQueryParams) (*interfaces.DatasetQueryResponse, error) {
	v.calls++
	v.request = params
	return v.response, v.err
}

type resourceQueryRowFilter struct{}

func (resourceQueryRowFilter) ResolveRowFilters(_ context.Context, refs []string) ([]interfaces.RowFilterDecisionEntry, error) {
	value := "00123"
	return []interfaces.RowFilterDecisionEntry{{ObjectTypeRef: refs[0], EffectiveRowFilterDigest: "sha256:restricted-resource",
		Predicate: interfaces.RowFilterPredicate{Kind: "in", Property: "notes", Values: []interfaces.RowFilterValue{{Type: "string", String: &value}}}}}, nil
}

func TestResourceQueryPreservesUniformParametersAndExactValues(t *testing.T) {
	vega := &resourceQueryVega{response: &interfaces.DatasetQueryResponse{Entries: []map[string]any{{
		"customer_id": json.Number("9007199254740993"), "phone": json.Number("1234567890123456.78"), "notes": "00123", "secret": nil,
	}}, QuerySource: "source", Paging: &interfaces.ResourceDataPagingResponse{}}}
	service := resourcePagingService(t, vega, 1)
	service.rowFilters = resourceQueryRowFilter{}
	query := resourcePagingQuery(7)
	query.Offset = 14
	query.NeedTotal = false
	query.Properties = []string{"id", "mobile", "notes", "secret"}
	query.ActualCondition = &cond.CondCfg{Operation: cond.OperationAnd, SubConds: []*cond.CondCfg{
		{Name: "id", Operation: cond.OperationEq, ValueFrom: cond.ValueFrom_Const, Value: json.Number("9007199254740993")},
		{Name: "mobile", Operation: cond.OperationGte, ValueFrom: cond.ValueFrom_Const, Value: json.Number("1234567890123456.78")},
	}}
	result, err := service.GetObjectsByObjectTypeID(resourcePagingContext(), query)
	if err != nil {
		t.Fatal(err)
	}
	if vega.calls != 1 || vega.request.Paging.Mode != "single" || vega.request.Paging.Offset != 14 || vega.request.Paging.Limit != 7 || vega.request.NeedTotal || len(vega.request.Sort) == 0 || len(vega.request.OutputFields) == 0 || vega.request.IgnoreLocalIndex == nil {
		t.Fatalf("unified Resource parameters changed: %#v", vega.request)
	}
	expected := map[string]any{"operation": "and", "sub_conditions": []any{
		map[string]any{"operation": "and", "sub_conditions": []any{
			map[string]any{"field": "customer_id", "operation": "==", "value_from": "const", "value": json.Number("9007199254740993")},
			map[string]any{"field": "phone", "operation": ">=", "value_from": "const", "value": json.Number("1234567890123456.78")},
		}},
		map[string]any{"field": "notes", "operation": "in", "value": []any{"00123"}},
	}}
	if !reflect.DeepEqual(vega.request.FilterCondition, expected) {
		t.Fatalf("conditions changed: %#v", vega.request.FilterCondition)
	}
	wire, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"id":9007199254740993`, `"mobile":1234567890123456.78`, `"notes":"00123"`, `"secret":null`} {
		if !strings.Contains(string(wire), expected) {
			t.Fatalf("final JSON %s missing %s", wire, expected)
		}
	}
}

func TestResourceQueryMapsFieldOperands(t *testing.T) {
	vega := &resourceQueryVega{response: &interfaces.DatasetQueryResponse{Entries: []map[string]any{}}}
	service := resourcePagingService(t, vega, 1)
	query := resourcePagingQuery(10)
	query.ActualCondition = &cond.CondCfg{Operation: cond.OperationAnd, SubConds: []*cond.CondCfg{
		{Name: "id", Operation: cond.OperationEq, ValueFrom: cond.ValueFrom_Field, Value: "mobile"},
		{Name: "notes", Operation: cond.OperationEq, ValueFrom: cond.ValueFrom_Const, Value: "mobile"},
	}}
	if _, err := service.GetObjectsByObjectTypeID(resourcePagingContext(), query); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(vega.request.FilterCondition)
	if err != nil {
		t.Fatal(err)
	}
	expected := `{"operation":"and","sub_conditions":[{"field":"customer_id","operation":"==","value_from":"field","value":"phone"},{"field":"notes","operation":"==","value_from":"const","value":"mobile"}]}`
	var actualCondition, expectedCondition any
	if err := json.Unmarshal(encoded, &actualCondition); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(expected), &expectedCondition); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actualCondition, expectedCondition) {
		t.Fatalf("field operands changed: %s", encoded)
	}
	if query.ActualCondition.SubConds[0].Value != "mobile" {
		t.Fatal("caller field reference changed")
	}
}

func TestResourceQueryPropagatesUnsupportedCapabilities(t *testing.T) {
	for _, mode := range []string{"list", "detail", "scan"} {
		for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotImplemented} {
			t.Run(fmt.Sprintf("%s/%d", mode, status), func(t *testing.T) {
				vega := &resourceQueryVega{err: interfaces.NewVegaDownstreamError(status, `{"error_details":"requested capability is unsupported"}`)}
				service := resourcePagingService(t, vega, 1)
				var err error
				switch mode {
				case "detail":
					_, err = service.GetObjectPropertyValue(resourcePagingContext(), &interfaces.ObjectPropertyValueQuery{KNID: "kn-1", Branch: "main", ObjectTypeID: "customer", InstanceIdentities: []map[string]any{{"id": "customer-1"}}, Properties: []string{"notes"}})
				case "scan":
					_, err = service.GetObjectsByObjectTypeIDForScan(resourcePagingContext(), resourcePagingQuery(10))
				default:
					_, err = service.GetObjectsByObjectTypeID(resourcePagingContext(), resourcePagingQuery(10))
				}
				var httpErr *httperrors.HTTPError
				expectedCalls := 1
				if mode == "scan" && status == http.StatusNotImplemented {
					// Preserve the established generic cursor-to-offset fallback.
					expectedCalls = 2
				}
				if !errors.As(err, &httpErr) || vega.calls != expectedCalls {
					t.Fatalf("error=%v data reads=%d", err, vega.calls)
				}
				// Existing BKN handling preserves Resource 4xx errors; non-client errors use its service boundary.
				expectedStatus := status
				if status >= 500 {
					expectedStatus = http.StatusInternalServerError
				}
				if httpErr.HTTPCode != expectedStatus {
					t.Fatalf("status=%d error=%v", httpErr.HTTPCode, err)
				}
				if len(vega.request.Sort) == 0 || len(vega.request.OutputFields) == 0 {
					t.Fatalf("query defaults were bypassed: %#v", vega.request)
				}
			})
		}
	}
}

func TestResourceDetailPreservesUnifiedFetchAndRows(t *testing.T) {
	for _, entries := range [][]map[string]any{
		{}, {{"customer_id": json.Number("0"), "notes": "first"}},
		{{"customer_id": json.Number("0"), "notes": "first"}, {"customer_id": json.Number("0"), "notes": "second"}},
		{{"customer_id": nil, "notes": "present"}},
	} {
		vega := &resourceQueryVega{response: &interfaces.DatasetQueryResponse{Entries: entries, QuerySource: "source"}}
		service := resourcePagingService(t, vega, 1)
		result, err := service.GetObjectPropertyValue(resourcePagingContext(), &interfaces.ObjectPropertyValueQuery{KNID: "kn-1", Branch: "main", ObjectTypeID: "customer", InstanceIdentities: []map[string]any{{"id": json.Number("0")}}, Properties: []string{"notes"}})
		if err != nil {
			t.Fatal(err)
		}
		if vega.calls != 1 || vega.request.Paging.Limit != interfaces.MAX_LIMIT || !vega.request.NeedTotal {
			t.Fatalf("detail fetch semantics changed: %#v", vega.request)
		}
		if result.Datas == nil || len(result.Datas) != len(entries) {
			t.Fatalf("detail rows changed: %#v", result.Datas)
		}
		for i, row := range entries {
			if result.Datas[i]["id"] != row["customer_id"] || result.Datas[i]["notes"] != row["notes"] {
				t.Fatalf("detail projection changed: %#v", result.Datas)
			}
		}
	}
}

func TestResourceQueryTotalCountPresence(t *testing.T) {
	for _, sample := range []bool{false, true} {
		for _, known := range []bool{false, true} {
			t.Run(fmt.Sprintf("sample=%t/known=%t", sample, known), func(t *testing.T) {
				var total *int64
				if known {
					total = new(int64(0))
				}
				vega := &resourceQueryVega{response: &interfaces.DatasetQueryResponse{Entries: []map[string]any{}, TotalCount: total}}
				service := resourcePagingService(t, vega, 1)
				query := resourcePagingQuery(10)
				var result any
				if sample {
					response, err := service.GetObjectTypeSampleData(resourcePagingContext(), query)
					if err != nil {
						t.Fatal(err)
					}
					result = response
				} else {
					response, err := service.GetObjectsByObjectTypeID(resourcePagingContext(), query)
					if err != nil {
						t.Fatal(err)
					}
					result = response
				}
				encoded, err := json.Marshal(result)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(encoded), `"total_count"`) != known {
					t.Fatalf("total presence changed: %s", encoded)
				}
			})
		}
	}
}

func TestResourceFalseRowPermissionReturnsEmptyArray(t *testing.T) {
	vega := &resourceQueryVega{}
	service := resourcePagingService(t, vega, 1)
	service.rowFilters = predicateRowFilterStub{predicate: interfaces.RowFilterPredicate{Kind: "false"}}
	query := resourcePagingQuery(10)
	result, err := service.GetObjectsByObjectTypeID(resourcePagingContext(), query)
	if err != nil {
		t.Fatal(err)
	}
	if result.Datas == nil || len(result.Datas) != 0 || vega.calls != 0 {
		t.Fatalf("data=%#v reads=%d", result.Datas, vega.calls)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"datas":[]`) || !strings.Contains(string(encoded), `"total_count":0`) || !result.RowFilterApplied {
		t.Fatalf("deny-all response changed: %s", encoded)
	}
}

func TestResourceGroupedCountPreservesKnownZero(t *testing.T) {
	vega := &resourceQueryVega{response: &interfaces.DatasetQueryResponse{Entries: []map[string]any{}}}
	service := resourcePagingService(t, vega, 1)
	result, err := service.AggregateObjectsByObjectTypeID(resourcePagingContext(), resourcePagingQuery(10), "", "count_rows", []string{"notes"})
	if err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalCount == nil || *result.TotalCount != 0 || !strings.Contains(string(wire), `"total_count":0`) {
		t.Fatalf("complete empty groups lost their known total: %s", wire)
	}
}

func TestResourceCountRequiresKnownTotal(t *testing.T) {
	for _, total := range []*int64{nil, new(int64(0)), new(int64(3))} {
		vega := &resourceQueryVega{response: &interfaces.DatasetQueryResponse{TotalCount: total}}
		service := resourcePagingService(t, vega, 1)
		result, err := service.AggregateObjectsByObjectTypeID(resourcePagingContext(), resourcePagingQuery(10), "", "count_rows", nil)
		if total == nil {
			if err == nil {
				t.Fatal("unknown count was treated as zero")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if result.TotalCount == nil || *result.TotalCount != *total || result.Datas[0]["__value"] != json.Number(fmt.Sprint(*total)) {
			t.Fatalf("known count changed: %#v", result)
		}
	}
}
