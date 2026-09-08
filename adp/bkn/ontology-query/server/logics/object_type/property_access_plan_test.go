// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package object_type

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"go.uber.org/mock/gomock"
	cond "ontology-query/common/condition"
	"ontology-query/common/maskrule"
	"ontology-query/interfaces"
	omock "ontology-query/interfaces/mock"
)

type propertyAccessStub struct {
	levels map[string]interfaces.PropertyAccessLevel
	calls  *int
}

func TestObjectQueryRejectsEmptyReturnBeforeProxyOrVega(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	vega := omock.NewMockVegaBackendAccess(ctrl)
	proxy := &objectTypeProxyResolverStub{}
	objectType := accessPlanObjectType()
	objectType.DataSource = &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "resource-1"}
	models.EXPECT().GetObjectType(gomock.Any(), "kn-1", "main", "customer").Return(objectType, true, nil)
	service := &objectTypeService{
		omAccess: models, vba: vega, proxy: proxy,
		propertyAccess: propertyAccessStub{levels: map[string]interfaces.PropertyAccessLevel{
			"id": interfaces.PropertyAccessSchema, "mobile": interfaces.PropertyAccessSchema,
			"notes": interfaces.PropertyAccessSchema, "secret": interfaces.PropertyAccessNone,
		}},
	}
	_, err := service.GetObjectsByObjectTypeID(context.Background(), &interfaces.ObjectQueryBaseOnObjectType{
		KNID: "kn-1", Branch: "main", ObjectTypeID: "customer", Properties: []string{"notes"},
		PageQuery: interfaces.PageQuery{Sort: []*interfaces.SortParams{{Field: interfaces.SORT_FIELD_SCORE, Direction: "desc"}}},
	})
	httpError, ok := err.(*rest.HTTPError)
	if !ok || httpError.HTTPCode != http.StatusForbidden || len(proxy.bindings) != 0 {
		t.Fatalf("result error = %#v, proxy bindings = %#v", err, proxy.bindings)
	}
}

func TestVegaAndOpenSearchUseSamePropertyProjection(t *testing.T) {
	ctrl := gomock.NewController(t)
	search := omock.NewMockOpenSearchAccess(ctrl)
	objectType := accessPlanObjectType()
	objectType.DataSource = &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "resource-1"}
	objectType.Status = &interfaces.ObjectTypeStatus{Index: "customer-index"}
	query := &interfaces.ObjectQueryBaseOnObjectType{
		KNID: "kn-1", Branch: "main", ObjectTypeID: "customer", Properties: []string{"id", "mobile"},
		PageQuery: interfaces.PageQuery{Limit: 10, Sort: []*interfaces.SortParams{{Field: "id", Direction: "asc"}}},
		CommonQueryParameters: interfaces.CommonQueryParameters{ExcludeSystemProperties: []string{
			interfaces.SYSTEM_PROPERTY_INSTANCE_ID, interfaces.SYSTEM_PROPERTY_INSTANCE_IDENTITY,
			interfaces.SYSTEM_PROPERTY_DISPLAY, interfaces.SORT_FIELD_SCORE,
		}},
	}
	plan, err := buildPropertyAccessPlan(context.Background(), propertyAccessStub{levels: map[string]interfaces.PropertyAccessLevel{
		"id": interfaces.PropertyAccessFull, "mobile": interfaces.PropertyAccessMasked,
		"notes": interfaces.PropertyAccessSchema, "secret": interfaces.PropertyAccessNone,
	}}, objectType, query, true)
	if err != nil {
		t.Fatal(err)
	}
	vega := &vegaStubForOTQuery{resp: &interfaces.DatasetQueryResponse{Entries: []map[string]any{
		{"customer_id": "customer-1", "phone": "13812345678", "notes": "raw-notes", "secret": "raw-secret"},
	}}}
	service := &objectTypeService{vba: vega, osa: search}
	var vegaResult interfaces.Objects
	if err := service.getObjectsFromResource(context.Background(), query, objectType, &vegaResult, plan.fieldPropertyMap(), plan); err != nil {
		t.Fatal(err)
	}
	search.EXPECT().SearchData(gomock.Any(), "customer-index", gomock.Any()).DoAndReturn(
		func(_ context.Context, _ string, dsl any) ([]interfaces.Hit, error) {
			fields, ok := dsl.(map[string]any)["_source"].([]string)
			if !ok || len(fields) != 2 {
				t.Fatalf("OpenSearch _source = %#v", dsl)
			}
			return []interfaces.Hit{{Source: map[string]any{
				"id": "customer-1", "mobile": "13812345678", "notes": "raw-notes", "secret": "raw-secret",
			}}}, nil
		})
	var searchResult interfaces.Objects
	if err := service.getObjectsFromObjectIndex(context.Background(), query, objectType, &searchResult,
		map[string]string{"id": "id", "mobile": "mobile"}, plan); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(vegaResult.Datas, searchResult.Datas) {
		t.Fatalf("Vega and OpenSearch differ: %#v %#v", vegaResult.Datas, searchResult.Datas)
	}
	if vega.lastParams == nil || len(vega.lastParams.OutputFields) != 2 {
		t.Fatalf("Vega output fields = %#v", vega.lastParams)
	}
}

func (stub propertyAccessStub) ResolvePropertyLevels(_ context.Context,
	items []interfaces.PropertyLevelsRequestItem) ([]interfaces.PropertyLevelsDecisionEntry, error) {
	if stub.calls != nil {
		*stub.calls = *stub.calls + 1
	}
	entries := make([]interfaces.PropertyLevelsDecisionEntry, 0, len(items))
	for _, item := range items {
		entry := interfaces.PropertyLevelsDecisionEntry{ObjectTypeRef: item.ObjectTypeRef}
		for _, name := range item.Properties {
			entry.Properties = append(entry.Properties, interfaces.PropertyAccessDecision{
				Name: name, Level: stub.levels[name], Source: "test",
			})
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func TestObjectQueryCursorReauthorizesAndNeverExposesRawPosition(t *testing.T) {
	ctrl := gomock.NewController(t)
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	objectType := accessPlanObjectType()
	objectType.DataSource = &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "resource-1"}
	models := omock.NewMockOntologyManagerAccess(ctrl)
	models.EXPECT().GetObjectType(gomock.Any(), "kn-1", "main", "customer").Times(2).
		Return(objectType, true, nil)
	vega := &vegaStubForOTQuery{resp: &interfaces.DatasetQueryResponse{
		Entries:     []map[string]any{{"customer_id": "customer-1", "phone": "13812345678"}},
		SearchAfter: []any{"raw-secret-position"},
	}}
	calls := 0
	levels := map[string]interfaces.PropertyAccessLevel{
		"id": interfaces.PropertyAccessFull, "mobile": interfaces.PropertyAccessMasked,
		"notes": interfaces.PropertyAccessSchema, "secret": interfaces.PropertyAccessNone,
	}
	access := propertyAccessStub{levels: levels, calls: &calls}
	service := &objectTypeService{
		omAccess: models, vba: vega, proxy: &objectTypeProxyResolverStub{},
		propertyAccess: access, cursor: testQueryCursorCodec(t, now),
	}
	ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY,
		interfaces.AccountInfo{ID: "user-1", Type: "user"})
	newQuery := func() *interfaces.ObjectQueryBaseOnObjectType {
		return &interfaces.ObjectQueryBaseOnObjectType{
			KNID: "kn-1", Branch: "main", ObjectTypeID: "customer", Properties: []string{"id", "mobile"},
			PageQuery: interfaces.PageQuery{Limit: 10, Sort: []*interfaces.SortParams{{Field: "id", Direction: "asc"}}},
		}
	}
	first, err := service.GetObjectsByObjectTypeID(ctx, newQuery())
	if err != nil || first.Cursor == "" || calls != 1 {
		t.Fatalf("first page = %#v, %v, auth calls = %d", first, err, calls)
	}
	body, err := json.Marshal(first)
	if err != nil || strings.Contains(string(body), "raw-secret-position") || strings.Contains(string(body), "search_after") {
		t.Fatalf("response leaked raw pagination position: %s, %v", body, err)
	}

	levels["mobile"] = interfaces.PropertyAccessSchema
	secondQuery := newQuery()
	secondQuery.Cursor = first.Cursor
	second, err := service.GetObjectsByObjectTypeID(ctx, secondQuery)
	if err != nil || calls != 2 {
		t.Fatalf("second page = %#v, %v, auth calls = %d", second, err, calls)
	}
	if len(vega.lastParams.SearchAfter) != 1 || vega.lastParams.SearchAfter[0] != "raw-secret-position" {
		t.Fatalf("restored downstream position = %#v", vega.lastParams.SearchAfter)
	}
	if _, exists := second.Datas[0]["mobile"]; exists {
		t.Fatalf("permission downgrade was not applied: %#v", second.Datas[0])
	}
}

func accessPlanObjectType() interfaces.ObjectType {
	keepStart, keepEnd := 1, 1
	return interfaces.ObjectType{
		KNID: "kn-1",
		ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
			OTID: "customer", PrimaryKeys: []string{"id"}, DisplayKey: "mobile",
			DataProperties: []cond.DataProperty{
				{Name: "id", Type: "string", MappedField: cond.Field{Name: "customer_id"}},
				{Name: "mobile", Type: "string", MappedField: cond.Field{Name: "phone"},
					MaskRule: &maskrule.Rule{Kind: maskrule.KindPartial, KeepStart: &keepStart, KeepEnd: &keepEnd, Replacement: "*"}},
				{Name: "notes", Type: "string", MappedField: cond.Field{Name: "notes"}},
				{Name: "secret", Type: "string", MappedField: cond.Field{Name: "secret"}},
			},
		},
	}
}

func TestPropertyAccessPlanProjectsAndMasksBeforeResponseAssembly(t *testing.T) {
	objectType := accessPlanObjectType()
	resolver := propertyAccessStub{levels: map[string]interfaces.PropertyAccessLevel{
		"id": interfaces.PropertyAccessFull, "mobile": interfaces.PropertyAccessMasked,
		"notes": interfaces.PropertyAccessSchema, "secret": interfaces.PropertyAccessNone,
	}}
	query := &interfaces.ObjectQueryBaseOnObjectType{
		Properties: []string{"id", "mobile"},
		PageQuery:  interfaces.PageQuery{Sort: []*interfaces.SortParams{{Field: "id", Direction: "asc"}}},
	}
	plan, err := buildPropertyAccessPlan(context.Background(), resolver, objectType, query, true)
	if err != nil {
		t.Fatalf("buildPropertyAccessPlan() error = %v", err)
	}
	fields := plan.fieldPropertyMap()
	if len(fields) != 2 || fields["customer_id"] != "id" || fields["phone"] != "mobile" {
		t.Fatalf("physical fields = %#v", fields)
	}
	got := plan.projectRow(map[string]any{
		"id": "customer-1", "mobile": "13812345678", "notes": "raw-notes", "secret": "raw-secret",
	}, &objectType, query)
	if got["id"] != "customer-1" || got["mobile"] != "1*********8" || got[interfaces.SYSTEM_PROPERTY_DISPLAY] != "1*********8" {
		t.Fatalf("projected row = %#v", got)
	}
	if _, exists := got["notes"]; exists {
		t.Fatal("schema-only value leaked")
	}
	if _, exists := got["secret"]; exists {
		t.Fatal("none value leaked")
	}
	if _, exists := got[interfaces.SYSTEM_PROPERTY_INSTANCE_IDENTITY]; !exists {
		t.Fatal("full primary key should permit instance identity")
	}
}

func TestPropertyAccessPlanDoesNotFetchPartialPrimaryKeyForSystemFields(t *testing.T) {
	objectType := accessPlanObjectType()
	objectType.PrimaryKeys = []string{"id", "secret"}
	resolver := propertyAccessStub{levels: map[string]interfaces.PropertyAccessLevel{
		"id": interfaces.PropertyAccessFull, "mobile": interfaces.PropertyAccessFull,
		"notes": interfaces.PropertyAccessSchema, "secret": interfaces.PropertyAccessNone,
	}}
	query := &interfaces.ObjectQueryBaseOnObjectType{
		Properties: []string{"mobile"},
		PageQuery:  interfaces.PageQuery{Sort: []*interfaces.SortParams{{Field: "id", Direction: "asc"}}},
	}
	plan, err := buildPropertyAccessPlan(context.Background(), resolver, objectType, query, true)
	if err != nil {
		t.Fatal(err)
	}
	fields := plan.fieldPropertyMap()
	if _, exists := fields["secret"]; exists {
		t.Fatalf("non-full primary key was fetched: %#v", fields)
	}
	if _, exists := fields["customer_id"]; exists {
		t.Fatalf("partial identity dependency was fetched: %#v", fields)
	}
	result := plan.projectRow(map[string]any{"mobile": "visible", "id": "id-1", "secret": "secret-1"}, &objectType, query)
	if _, exists := result[interfaces.SYSTEM_PROPERTY_INSTANCE_IDENTITY]; exists {
		t.Fatalf("partial primary key produced identity: %#v", result)
	}
}

func TestPropertyAccessPlanHidesSchemaAndDowngradesInvalidMaskRule(t *testing.T) {
	objectType := accessPlanObjectType()
	objectType.DataProperties[1].MaskRule = nil
	objectType.PrimaryKeys = []string{"id", "secret"}
	objectType.DisplayKey = "secret"
	objectType.LogicProperties = []*interfaces.LogicProperty{
		{Name: "visible_logic", Parameters: []interfaces.Parameter{{ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_PROP, Value: "id"}}},
		{Name: "hidden_logic", Parameters: []interfaces.Parameter{{ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_PROP, Value: "secret"}}},
	}
	resolver := propertyAccessStub{levels: map[string]interfaces.PropertyAccessLevel{
		"id": interfaces.PropertyAccessFull, "mobile": interfaces.PropertyAccessMasked,
		"notes": interfaces.PropertyAccessSchema, "secret": interfaces.PropertyAccessNone,
	}}
	plan, err := buildPropertyAccessPlan(context.Background(), resolver, objectType, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if plan.effective["mobile"] != interfaces.PropertyAccessSchema {
		t.Fatalf("invalid mask effective level = %q", plan.effective["mobile"])
	}
	filtered := plan.filterObjectType(objectType)
	if len(filtered.DataProperties) != 3 {
		t.Fatalf("visible schema properties = %#v", filtered.DataProperties)
	}
	if !reflect.DeepEqual(filtered.PrimaryKeys, []string{"id"}) || filtered.DisplayKey != "" ||
		len(filtered.LogicProperties) != 1 || filtered.LogicProperties[0].Name != "visible_logic" {
		t.Fatalf("filtered object type metadata = %#v", filtered.ObjectTypeWithKeyField)
	}
	response := &interfaces.ResourceSchemaResponse{SchemaDefinition: []map[string]any{
		{"name": "customer_id"}, {"name": "phone"}, {"name": "notes"}, {"name": "secret"}, {"name": "physical_only"},
	}}
	plan.filterResourceSchema(response)
	if len(response.SchemaDefinition) != 3 {
		t.Fatalf("filtered resource schema = %#v", response.SchemaDefinition)
	}
}

func TestPropertyAccessPlanRejectsHiddenAndMissingPropertiesIndistinguishably(t *testing.T) {
	objectType := accessPlanObjectType()
	resolver := propertyAccessStub{levels: map[string]interfaces.PropertyAccessLevel{
		"id": interfaces.PropertyAccessFull, "mobile": interfaces.PropertyAccessFull,
		"notes": interfaces.PropertyAccessSchema, "secret": interfaces.PropertyAccessNone,
	}}
	errorFor := func(property string) *rest.HTTPError {
		query := &interfaces.ObjectQueryBaseOnObjectType{Properties: []string{property}}
		_, err := buildPropertyAccessPlan(context.Background(), resolver, objectType, query, true)
		httpError, ok := err.(*rest.HTTPError)
		if !ok {
			t.Fatalf("property %q error = %#v", property, err)
		}
		return httpError
	}
	hidden := errorFor("secret")
	missing := errorFor("does_not_exist")
	if hidden.HTTPCode != missing.HTTPCode || hidden.BaseError.ErrorCode != missing.BaseError.ErrorCode ||
		hidden.BaseError.ErrorDetails != missing.BaseError.ErrorDetails {
		t.Fatalf("hidden and missing errors differ: %#v %#v", hidden, missing)
	}
}

func TestPropertyAccessPlanRejectsNonFullOperationAndEmptyReturnBeforeRead(t *testing.T) {
	objectType := accessPlanObjectType()
	resolver := propertyAccessStub{levels: map[string]interfaces.PropertyAccessLevel{
		"id": interfaces.PropertyAccessFull, "mobile": interfaces.PropertyAccessMasked,
		"notes": interfaces.PropertyAccessSchema, "secret": interfaces.PropertyAccessNone,
	}}
	query := &interfaces.ObjectQueryBaseOnObjectType{
		Properties: []string{"id"},
		PageQuery:  interfaces.PageQuery{Sort: []*interfaces.SortParams{{Field: "mobile", Direction: "asc"}}},
	}
	_, err := buildPropertyAccessPlan(context.Background(), resolver, objectType, query, true)
	httpError, ok := err.(*rest.HTTPError)
	if !ok || httpError.HTTPCode != http.StatusBadRequest {
		t.Fatalf("operation error = %#v", err)
	}

	query = &interfaces.ObjectQueryBaseOnObjectType{Properties: []string{"notes"}}
	_, err = buildPropertyAccessPlan(context.Background(), resolver, objectType, query, true)
	httpError, ok = err.(*rest.HTTPError)
	if !ok || httpError.HTTPCode != http.StatusForbidden {
		t.Fatalf("empty return error = %#v", err)
	}
}

func TestPropertyAccessPlanTreatsEmptyObjectQueryPropertiesAsAllFields(t *testing.T) {
	objectType := accessPlanObjectType()
	objectType.LogicProperties = []*interfaces.LogicProperty{{
		Name: "mobile_label",
		Parameters: []interfaces.Parameter{{
			ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_PROP,
			Value:     "mobile",
		}},
	}}
	query := &interfaces.ObjectQueryBaseOnObjectType{
		CommonQueryParameters: interfaces.CommonQueryParameters{IncludeLogicParams: true},
		ObjectQueryInfo: &interfaces.ObjectQueryInfo{
			InstanceIdentity: []map[string]any{{"id": "customer-1"}},
		},
	}
	plan, err := buildPropertyAccessPlan(context.Background(), propertyAccessStub{levels: map[string]interfaces.PropertyAccessLevel{
		"id": interfaces.PropertyAccessFull, "mobile": interfaces.PropertyAccessFull,
		"notes": interfaces.PropertyAccessSchema, "secret": interfaces.PropertyAccessNone,
	}}, objectType, query, true)
	if err != nil {
		t.Fatalf("buildPropertyAccessPlan() error = %v", err)
	}
	if _, exists := plan.returnData["mobile"]; !exists {
		t.Fatalf("empty object property selection did not include data fields: %#v", plan.returnData)
	}
	if _, exists := plan.returnLogic["mobile_label"]; !exists {
		t.Fatalf("include_logic_params did not include logic fields: %#v", plan.returnLogic)
	}
	if _, exists := plan.fetchFields["mobile"]; !exists {
		t.Fatalf("action parameter dependency was not fetched: %#v", plan.fetchFields)
	}
}

func TestObjectQueryDefaultSortDoesNotRequireFullPrimaryKeyAccess(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	vega := omock.NewMockVegaBackendAccess(ctrl)
	objectType := accessPlanObjectType()
	objectType.DataSource = &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "resource-1"}
	models.EXPECT().GetObjectType(gomock.Any(), "kn-1", "main", "customer").Return(objectType, true, nil)
	vega.EXPECT().QueryResourceData(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ string, params *interfaces.ResourceDataQueryParams) (*interfaces.DatasetQueryResponse, error) {
			if len(params.Sort) == 0 {
				t.Fatal("default sort was not added after authorization")
			}
			return &interfaces.DatasetQueryResponse{Entries: []map[string]any{{"phone": "visible"}}}, nil
		})
	service := &objectTypeService{
		omAccess: models, vba: vega, proxy: &objectTypeProxyResolverStub{},
		propertyAccess: propertyAccessStub{levels: map[string]interfaces.PropertyAccessLevel{
			"id": interfaces.PropertyAccessSchema, "mobile": interfaces.PropertyAccessFull,
			"notes": interfaces.PropertyAccessSchema, "secret": interfaces.PropertyAccessNone,
		}},
	}
	result, err := service.GetObjectsByObjectTypeID(context.Background(), &interfaces.ObjectQueryBaseOnObjectType{
		KNID: "kn-1", Branch: "main", ObjectTypeID: "customer", Properties: []string{"mobile"},
		PageQuery: interfaces.PageQuery{Limit: 10},
	})
	if err != nil {
		t.Fatalf("GetObjectsByObjectTypeID() error = %v", err)
	}
	if len(result.Datas) != 1 || result.Datas[0]["mobile"] != "visible" {
		t.Fatalf("result = %#v", result)
	}
	if _, exists := result.Datas[0][interfaces.SYSTEM_PROPERTY_INSTANCE_IDENTITY]; exists {
		t.Fatalf("schema-only primary key produced identity: %#v", result.Datas[0])
	}
}
