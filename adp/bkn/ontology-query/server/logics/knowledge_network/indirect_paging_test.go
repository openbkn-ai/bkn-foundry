// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledge_network

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common"
	cond "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common/condition"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

// The fixture of #2660: six campaigns reach customers 1..10001 through a
// campaign_customers table of 30004 pairs.
var campaignCustomerIDs = map[int][]int{
	101: {1, 10000, 10001},
	102: idRange(1, 9999),
	103: idRange(1, 10000),
	104: idRange(1, 10001),
	105: {10001},
	106: {},
}

func idRange(first, last int) []int {
	ids := make([]int, 0, last-first+1)
	for id := first; id <= last; id++ {
		ids = append(ids, id)
	}
	return ids
}

func campaignCustomerPairs() []map[string]any {
	rows := []map[string]any{}
	for campaign, customers := range campaignCustomerIDs {
		for _, customer := range customers {
			rows = append(rows, map[string]any{"campaign_id": campaign, "customer_id": customer})
		}
	}
	return rows
}

func campaignCustomerMapping() *interfaces.InDirectMapping {
	return &interfaces.InDirectMapping{
		BackingDataSource: resourceBacking("campaign_customers"),
		SourceMappingRules: []interfaces.Mapping{{
			SourceProp: interfaces.SimpleProperty{Name: "campaign_id"},
			TargetProp: interfaces.SimpleProperty{Name: "campaign_id"},
		}},
		TargetMappingRules: []interfaces.Mapping{{
			SourceProp: interfaces.SimpleProperty{Name: "customer_id"},
			TargetProp: interfaces.SimpleProperty{Name: "customer_id"},
		}},
	}
}

// backingTableVega serves a resource table the way Vega does: it applies the
// filter and sort, and pages by single offset or by cursor.
type backingTableVega struct {
	rows    []map[string]any
	history []*interfaces.ResourceDataQueryParams
	served  int
	cursors map[string][]map[string]any
	results map[string][]map[string]any
}

func (v *backingTableVega) QueryResourceData(_ context.Context, _ string,
	params *interfaces.ResourceDataQueryParams) (*interfaces.DatasetQueryResponse, error) {
	v.history = append(v.history, params)
	var matched []map[string]any
	offset := params.Paging.Offset
	if params.Paging.Cursor != "" {
		matched = v.cursors[params.Paging.Cursor]
		offset, _ = strconv.Atoi(strings.TrimPrefix(params.Paging.Cursor, fmt.Sprintf("%p:", matched)))
	} else {
		key := fmt.Sprint(params.FilterCondition, params.Sort)
		if cached, exists := v.results[key]; exists {
			matched = cached
		} else {
			for _, row := range v.rows {
				if matchesFilter(params.FilterCondition, row) {
					matched = append(matched, row)
				}
			}
			// The fixture keys are all integers.
			sort.SliceStable(matched, func(i, j int) bool {
				for _, field := range params.Sort {
					left, right := matched[i][field.Field].(int), matched[j][field.Field].(int)
					if left != right {
						return left < right
					}
				}
				return false
			})
			if v.results == nil {
				v.results = map[string][]map[string]any{}
			}
			v.results[key] = matched
		}
	}
	limit := params.Paging.Limit
	if params.Paging.Cursor != "" {
		limit = interfaces.MAX_LIMIT
	}
	end := min(offset+limit, len(matched))
	page := matched[min(offset, len(matched)):end]
	v.served += len(page)
	resp := &interfaces.DatasetQueryResponse{Entries: page, TotalCount: int64(len(matched)),
		Paging: &interfaces.ResourceDataPagingResponse{}}
	if (params.Paging.Mode == interfaces.ResourceDataPagingModeCursor || params.Paging.Cursor != "") && end < len(matched) {
		if v.cursors == nil {
			v.cursors = map[string][]map[string]any{}
		}
		next := fmt.Sprintf("%p:%d", matched, end)
		v.cursors[next] = matched
		resp.Paging.NextCursor = &next
	}
	return resp, nil
}

func (v *backingTableVega) GetResourceSchema(context.Context, string) (*interfaces.ResourceSchemaResponse, error) {
	return &interfaces.ResourceSchemaResponse{}, nil
}

// matchesFilter evaluates the filter_condition JSON sent to Vega.
func matchesFilter(filter map[string]any, row map[string]any) bool {
	if filter == nil {
		return true
	}
	subConditions, _ := filter["sub_conditions"].([]any)
	switch filter["operation"] {
	case cond.OperationAnd, cond.OperationOr:
		isAnd := filter["operation"] == cond.OperationAnd
		for _, sub := range subConditions {
			if matchesFilter(sub.(map[string]any), row) != isAnd {
				return !isAnd
			}
		}
		return isAnd
	case "in":
		for _, value := range filter["value"].([]any) {
			if fmt.Sprint(value) == fmt.Sprint(row[filter["field"].(string)]) {
				return true
			}
		}
		return false
	}
	panic(fmt.Sprintf("unsupported filter %v", filter))
}

// customerObjects serves object queries over customers 1..10001 and the six
// campaigns, evaluating the conditions object reads receive.
type customerObjects struct {
	extraCustomers []int
	queries        []*interfaces.ObjectQueryBaseOnObjectType
}

func (o *customerObjects) GetObjectsByObjectTypeID(_ context.Context,
	query *interfaces.ObjectQueryBaseOnObjectType) (interfaces.Objects, error) {
	o.queries = append(o.queries, query)
	key, ids := "customer_id", append(idRange(1, 10001), o.extraCustomers...)
	if query.ObjectTypeID == "campaign" {
		key, ids = "campaign_id", []int{101, 102, 103, 104, 105, 106}
	}
	objects := interfaces.Objects{ObjectType: &interfaces.ObjectType{
		ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: query.ObjectTypeID, PrimaryKeys: []string{key}}}}
	for _, id := range ids {
		row := map[string]any{key: id, interfaces.SYSTEM_PROPERTY_INSTANCE_ID: fmt.Sprintf("%s-%d", query.ObjectTypeID, id)}
		if matchesCondition(query.ActualCondition, row) {
			objects.Datas = append(objects.Datas, row)
		}
	}
	if query.Limit > 0 && len(objects.Datas) > query.Limit {
		objects.Datas = objects.Datas[:query.Limit]
	}
	return objects, nil
}

// ScanObjectsByObjectTypeID keeps the fake usable once the object service can
// scan a whole match set; relation steps never call it.
func (o *customerObjects) ScanObjectsByObjectTypeID(context.Context, *interfaces.ObjectQueryBaseOnObjectType, int) (interfaces.Objects, error) {
	return interfaces.Objects{}, errors.New("not used")
}

func (o *customerObjects) GetObjectTypeSchema(context.Context, string, string, string) (*interfaces.ResourceSchemaResponse, error) {
	return nil, errors.New("not used")
}

func (o *customerObjects) GetObjectTypeSampleData(context.Context, *interfaces.ObjectQueryBaseOnObjectType) (*interfaces.ObjectTypeSampleData, error) {
	return nil, errors.New("not used")
}

func (o *customerObjects) GetObjectPropertyValue(context.Context, *interfaces.ObjectPropertyValueQuery) (interfaces.Objects, error) {
	return interfaces.Objects{}, errors.New("not used")
}

var inSets = map[*cond.CondCfg]map[int]struct{}{}

func inSet(condition *cond.CondCfg) map[int]struct{} {
	set, exists := inSets[condition]
	if !exists {
		set = map[int]struct{}{}
		for _, candidate := range condition.Value.([]any) {
			number, _ := strconv.Atoi(fmt.Sprint(candidate))
			set[number] = struct{}{}
		}
		inSets[condition] = set
	}
	return set
}

func matchesCondition(condition *cond.CondCfg, row map[string]any) bool {
	if condition == nil {
		return true
	}
	value, _ := row[condition.Name].(int)
	bound, _ := condition.Value.(int)
	switch condition.Operation {
	case cond.OperationAnd, cond.OperationOr:
		isAnd := condition.Operation == cond.OperationAnd
		for _, sub := range condition.SubConds {
			if matchesCondition(sub, row) != isAnd {
				return !isAnd
			}
		}
		return isAnd
	case "in":
		_, exists := inSet(condition)[value]
		return exists
	case "==":
		return value == bound
	case "<=":
		return value <= bound
	case ">=":
		return value >= bound
	}
	panic(fmt.Sprintf("unsupported condition %s", condition.Operation))
}

type indirectFixture struct {
	service *knowledgeNetworkService
	vega    *backingTableVega
	objects *customerObjects
	edge    *interfaces.TypeEdge
}

func newIndirectFixture(rows []map[string]any) *indirectFixture {
	vega := &backingTableVega{rows: rows}
	objects := &customerObjects{}
	return &indirectFixture{
		service: &knowledgeNetworkService{appSetting: &common.AppSetting{}, proxy: &knowledgeNetworkProxyResolverStub{},
			vba: vega, ots: objects},
		vega: vega, objects: objects,
		edge: &interfaces.TypeEdge{
			RelationTypeId: "campaign_reaches_customer",
			RelationType: interfaces.RelationType{
				RTID: "campaign_reaches_customer", RTName: "campaign_reaches_customer", Type: "indirect",
				SourceObjectTypeID: "campaign", TargetObjectTypeID: "customer",
				MappingRules: campaignCustomerMapping(),
			},
		},
	}
}

func campaignObjects(ids ...int) []interfaces.LevelObject {
	objects := make([]interfaces.LevelObject, len(ids))
	for index, id := range ids {
		objects[index] = interfaces.LevelObject{ObjectID: fmt.Sprintf("campaign-%d", id),
			ObjectData: map[string]any{"campaign_id": id}}
	}
	return objects
}

func customerObjectsOf(ids ...int) []interfaces.LevelObject {
	objects := make([]interfaces.LevelObject, len(ids))
	for index, id := range ids {
		objects[index] = interfaces.LevelObject{ObjectID: fmt.Sprintf("customer-%d", id),
			ObjectData: map[string]any{"customer_id": id}}
	}
	return objects
}

func reachedIDs(objects interfaces.Objects, key string) []int {
	ids := make([]int, 0, len(objects.Datas))
	for _, object := range objects.Datas {
		ids = append(ids, object[key].(int))
	}
	sort.Ints(ids)
	return ids
}

func pathQuery() *interfaces.SubGraphQueryBaseOnSource {
	return &interfaces.SubGraphQueryBaseOnSource{KNID: "ircap", Branch: interfaces.MAIN_BRANCH,
		CommonQueryParameters: interfaces.CommonQueryParameters{IgnoreLocalIndex: true}}
}

func exactCustomer(id int) interfaces.ObjectTypeWithKeyField {
	return interfaces.ObjectTypeWithKeyField{OTID: "customer", PageQuery: interfaces.PageQuery{Limit: 2},
		ActualCondition: &cond.CondCfg{Name: "customer_id", Operation: "==", Value: id}}
}

// Regression for #2660: the issue's nine exact campaign -> customer queries.
// 104 -> 10001 used to return no path because only the first 10000 backing
// rows of campaign 104 were read.
func TestIndirectRelationFindsExactPairsAtTheBackingPageBoundary(t *testing.T) {
	cases := []struct{ campaign, customer, paths int }{
		{101, 10001, 1}, {102, 9999, 1}, {103, 10000, 1}, {104, 1, 1}, {104, 10000, 1},
		{104, 10001, 1}, {105, 10001, 1}, {106, 10001, 0}, {103, 10001, 0},
	}
	for _, pushdown := range []bool{true, false} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("pushdown=%t/%d->%d", pushdown, tc.campaign, tc.customer), func(t *testing.T) {
				if !pushdown {
					original := maxPushdownTargets
					maxPushdownTargets = 0
					defer func() { maxPushdownTargets = original }()
				}
				fixture := newIndirectFixture(campaignCustomerPairs())
				fixture.edge.Direction = interfaces.DIRECTION_FORWARD
				result, err := fixture.service.getNextObjectsBatchByRelation(context.Background(), pathQuery(),
					campaignObjects(tc.campaign), fixture.edge, exactCustomer(tc.customer))
				if err != nil {
					t.Fatalf("expand: %v", err)
				}
				got := reachedIDs(result[fmt.Sprintf("campaign-%d", tc.campaign)], "customer_id")
				if len(got) != tc.paths || (tc.paths == 1 && got[0] != tc.customer) {
					t.Fatalf("reached %v, want %d path(s) to customer %d", got, tc.paths, tc.customer)
				}
				// The exact target condition is pushed into the backing read.
				if pushdown && fixture.vega.served > 1 {
					t.Fatalf("read %d backing rows for one exact pair", fixture.vega.served)
				}
			})
		}
	}
}

// Without a pushable target condition the backing rows are paged in a stable
// order until the matches run out, across the page boundary.
func TestIndirectRelationPagesBackingRowsWhenTheConditionCannotBePushedDown(t *testing.T) {
	fixture := newIndirectFixture(campaignCustomerPairs())
	fixture.edge.Direction = interfaces.DIRECTION_FORWARD
	// Matches customers 1..1000 and 10001: too many to resolve up front.
	broad := interfaces.ObjectTypeWithKeyField{OTID: "customer", PageQuery: interfaces.PageQuery{Limit: 5000},
		ActualCondition: &cond.CondCfg{Operation: cond.OperationOr, SubConds: []*cond.CondCfg{
			{Name: "customer_id", Operation: "<=", Value: 1000},
			{Name: "customer_id", Operation: ">=", Value: 10001},
		}}}
	result, err := fixture.service.getNextObjectsBatchByRelation(context.Background(), pathQuery(),
		campaignObjects(102, 103, 104), fixture.edge, broad)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	for campaign, want := range map[int]int{102: 1000, 103: 1000, 104: 1001} {
		got := reachedIDs(result[fmt.Sprintf("campaign-%d", campaign)], "customer_id")
		if len(got) != want {
			t.Fatalf("campaign %d reached %d customers, want %d", campaign, len(got), want)
		}
	}
	if got := reachedIDs(result["campaign-104"], "customer_id"); got[len(got)-1] != 10001 {
		t.Fatalf("campaign 104 missed customer 10001: last %d", got[len(got)-1])
	}
	if got := reachedIDs(result["campaign-103"], "customer_id"); got[len(got)-1] == 10001 {
		t.Fatal("campaign 103 must not reach customer 10001")
	}
	// 30000 backing rows: a single first page, then a cursor reopened from the
	// start and followed twice.
	if fixture.vega.served != 40000 || len(fixture.vega.history) != 4 {
		t.Fatalf("served %d rows in %d requests", fixture.vega.served, len(fixture.vega.history))
	}
	first, reopened := fixture.vega.history[0], fixture.vega.history[1]
	if first.Paging.Mode != interfaces.ResourceDataPagingModeSingle ||
		reopened.Paging.Mode != interfaces.ResourceDataPagingModeCursor || reopened.Paging.Offset != 0 {
		t.Fatalf("paging = %#v then %#v", first.Paging, reopened.Paging)
	}
	if len(first.Sort) != 2 || first.Sort[0].Field != "campaign_id" || first.Sort[1].Field != "customer_id" {
		t.Fatalf("backing sort is not a total order: %#v", first.Sort)
	}
}

// Reading stops once the path's object limit is filled, however large the
// backing table is: the first page is enough and no cursor is opened.
func TestIndirectRelationStopsReadingOnceTheLimitIsFilled(t *testing.T) {
	fixture := newIndirectFixture(campaignCustomerPairs())
	fixture.edge.Direction = interfaces.DIRECTION_FORWARD
	anyCustomer := interfaces.ObjectTypeWithKeyField{OTID: "customer", PageQuery: interfaces.PageQuery{Limit: 2}}
	result, err := fixture.service.getNextObjectsBatchByRelation(context.Background(), pathQuery(),
		campaignObjects(104), fixture.edge, anyCustomer)
	if err != nil || len(result["campaign-104"].Datas) != 2 {
		t.Fatalf("expand = %v, %v", result, err)
	}
	if len(fixture.vega.history) != 1 || fixture.vega.history[0].Paging.Mode != interfaces.ResourceDataPagingModeSingle {
		t.Fatalf("read %d backing pages, want one stateless page", len(fixture.vega.history))
	}
}

func TestIndirectRelationReverseDirection(t *testing.T) {
	fixture := newIndirectFixture(campaignCustomerPairs())
	fixture.edge.Direction = interfaces.DIRECTION_BACKWARD
	exactCampaign := interfaces.ObjectTypeWithKeyField{OTID: "campaign", PageQuery: interfaces.PageQuery{Limit: 2},
		ActualCondition: &cond.CondCfg{Name: "campaign_id", Operation: "==", Value: 104}}
	result, err := fixture.service.getNextObjectsBatchByRelation(context.Background(), pathQuery(),
		customerObjectsOf(10001), fixture.edge, exactCampaign)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if got := reachedIDs(result["customer-10001"], "campaign_id"); len(got) != 1 || got[0] != 104 {
		t.Fatalf("customer 10001 reached campaigns %v, want [104]", got)
	}

	fixture = newIndirectFixture(campaignCustomerPairs())
	fixture.edge.Direction = interfaces.DIRECTION_BACKWARD
	anyCampaign := interfaces.ObjectTypeWithKeyField{OTID: "campaign", PageQuery: interfaces.PageQuery{Limit: 10}}
	result, err = fixture.service.getNextObjectsBatchByRelation(context.Background(), pathQuery(),
		customerObjectsOf(10001), fixture.edge, anyCampaign)
	if got := reachedIDs(result["customer-10001"], "campaign_id"); err != nil || fmt.Sprint(got) != "[101 104 105]" {
		t.Fatalf("customer 10001 reached campaigns %v, %v", got, err)
	}
}

// A step that would have to read past the backing-row bound fails instead of
// returning a partial relation set as if it were complete.
func TestIndirectRelationFailsPastTheBackingRowBound(t *testing.T) {
	rows := make([]map[string]any, 0, maxIndirectBackingRows+1)
	for customer := 1; customer <= maxIndirectBackingRows+1; customer++ {
		rows = append(rows, map[string]any{"campaign_id": 107, "customer_id": customer})
	}
	fixture := newIndirectFixture(rows)
	fixture.edge.Direction = interfaces.DIRECTION_FORWARD
	// The condition only matches customers outside the backing table and too
	// many to push down, so nothing fills the limit early.
	fixture.objects.extraCustomers = idRange(200001, 201001)
	unreachable := interfaces.ObjectTypeWithKeyField{OTID: "customer", PageQuery: interfaces.PageQuery{Limit: 2},
		ActualCondition: &cond.CondCfg{Name: "customer_id", Operation: ">=", Value: 200001}}
	_, err := fixture.service.getNextObjectsBatchByRelation(context.Background(), pathQuery(),
		campaignObjects(107), fixture.edge, unreachable)
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusBadRequest ||
		!strings.Contains(fmt.Sprint(httpErr.ErrorDetails), "more than 100000 backing rows") {
		t.Fatalf("error = %v", err)
	}
}

// Matching known objects reads only the backing rows that reach the given
// targets.
func TestIndirectRelationMatchesKnownObjectsWithATargetFilter(t *testing.T) {
	fixture := newIndirectFixture(campaignCustomerPairs())
	relations, err := fixture.service.matchIndirectRelations(context.Background(),
		&interfaces.SubGraphQueryBaseOnObjects{KNID: "ircap", Branch: interfaces.MAIN_BRANCH},
		campaignObjects(104, 106), customerObjectsOf(10001), fixture.edge)
	if err != nil || len(relations) != 1 || relations[0].SourceObjectId != "campaign-104" ||
		relations[0].TargetObjectId != "customer-10001" {
		t.Fatalf("relations = %#v, %v", relations, err)
	}
	if fixture.vega.served != 1 {
		t.Fatalf("read %d backing rows for one known pair", fixture.vega.served)
	}
}
