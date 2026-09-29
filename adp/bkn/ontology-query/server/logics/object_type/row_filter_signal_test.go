// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package object_type

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	cond "ontology-query/common/condition"
	"ontology-query/interfaces"
	omock "ontology-query/interfaces/mock"
)

type predicateRowFilterStub struct {
	predicate interfaces.RowFilterPredicate
}

func (s predicateRowFilterStub) ResolveRowFilters(_ context.Context,
	refs []string) ([]interfaces.RowFilterDecisionEntry, error) {
	entries := make([]interfaces.RowFilterDecisionEntry, 0, len(refs))
	for _, ref := range refs {
		entries = append(entries, interfaces.RowFilterDecisionEntry{
			ObjectTypeRef: ref, Predicate: s.predicate,
			EffectiveRowFilterDigest: "sha256:test-row-filter-" + s.predicate.Kind,
		})
	}
	return entries, nil
}

func TestObjectQueryReportsWhetherRowFilterNarrowedResult(t *testing.T) {
	customer := "customer-1"
	cases := []struct {
		name      string
		predicate interfaces.RowFilterPredicate
		want      bool
		wantReads int
	}{
		{name: "unrestricted", predicate: interfaces.RowFilterPredicate{Kind: "true"}, want: false, wantReads: 1},
		{name: "partial", predicate: interfaces.RowFilterPredicate{
			Kind: "in", Property: "id", Values: []interfaces.RowFilterValue{{Type: "string", String: &customer}},
		}, want: true, wantReads: 1},
		{name: "deny all rows", predicate: interfaces.RowFilterPredicate{Kind: "false"}, want: true, wantReads: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			objectType := accessPlanObjectType()
			objectType.DataSource = &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "resource-1"}
			// Row filters compile only against exactly filterable properties.
			objectType.DataProperties[0].ConditionOperations = []string{cond.OperationIn}
			models := omock.NewMockOntologyManagerAccess(ctrl)
			models.EXPECT().GetObjectType(gomock.Any(), "kn-1", "main", "customer").Return(objectType, true, nil)
			vega := &vegaStubForOTQuery{resp: &interfaces.DatasetQueryResponse{
				Entries: []map[string]any{{"customer_id": customer}},
				Paging:  &interfaces.ResourceDataPagingResponse{},
			}}
			service := &objectTypeService{
				omAccess: models, vba: vega, proxy: &objectTypeProxyResolverStub{},
				propertyAccess: fullPropertyAccessStub{}, rowFilters: predicateRowFilterStub{predicate: tc.predicate},
				cursor: testQueryCursorCodec(t, time.Now()),
			}

			result, err := service.GetObjectsByObjectTypeID(context.Background(), &interfaces.ObjectQueryBaseOnObjectType{
				KNID: "kn-1", Branch: "main", ObjectTypeID: "customer", Properties: []string{"id"},
				PageQuery: interfaces.PageQuery{Limit: 10},
			})
			if err != nil {
				t.Fatalf("GetObjectsByObjectTypeID() error = %v", err)
			}
			if result.RowFilterApplied != tc.want {
				t.Fatalf("RowFilterApplied = %v, want %v", result.RowFilterApplied, tc.want)
			}
			if len(vega.paramsHistory) != tc.wantReads {
				t.Fatalf("resource reads = %d, want %d", len(vega.paramsHistory), tc.wantReads)
			}

			body, err := json.Marshal(result)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			var wire map[string]any
			if err := json.Unmarshal(body, &wire); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			if got, present := wire["row_filter_applied"]; present != tc.want || (present && got != true) {
				t.Fatalf("row_filter_applied on the wire = %v (present=%v), want present=%v", got, present, tc.want)
			}
		})
	}
}
