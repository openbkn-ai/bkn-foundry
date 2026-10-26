// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package binding

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	qm "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
)

func TestBuildResourceDataQuery(t *testing.T) {
	t.Run("API window stays outside relational IR", func(t *testing.T) {
		ignore := true
		mode := interfaces.BinaryModeMetadata
		params := &interfaces.ResourceDataQueryParams{Paging: interfaces.PagingRequest{Offset: 40, Limit: 20}, OutputFields: []string{"id"}, NeedTotal: true, Timeout: time.Second, IgnoreLocalIndex: &ignore, BinaryMode: &mode}
		query, err := BuildResourceDataQuery(params, "orders")
		require.NoError(t, err)
		assert.Equal(t, interfaces.PagingRequest{Offset: 40, Limit: 20}, params.Paging)
		assert.True(t, params.NeedTotal)
		assert.Equal(t, time.Second, params.Timeout)
		assert.Equal(t, interfaces.BinaryModeMetadata, *params.BinaryMode)
		parts := query
		scan := parts.FromPart.(*qm.Scan)
		assert.Equal(t, "orders", scan.ResourceID)
		assert.Equal(t, "output_fields[0]", parts.ColumnsPart[0].Origin.Path)
		assert.Empty(t, params.Paging.Mode)
		params.OutputFields[0] = "changed"
		assert.Equal(t, []string{"id"}, parts.ColumnsPart[0].Expr.Name)
	})
	t.Run("aggregate aliases and count remain inside output cropping", func(t *testing.T) {
		params := &interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Aggr: "min", Property: "amount", Alias: "name"}, GroupBy: []*interfaces.GroupByItem{{Property: "id"}}, Having: &interfaces.HavingClause{Field: "__value", Operation: ">", Value: json.Number("1.2300")}, Sort: []*interfaces.SortField{{Field: "name", Direction: "desc"}}, OutputFields: []string{"name"}}
		query, err := BuildResourceDataQuery(params, "orders")
		require.NoError(t, err)
		crop := query
		inner := crop.FromPart.(*qm.Subquery).Query
		assert.Len(t, inner.ColumnsPart, 2)
		assert.Len(t, inner.GroupByPart, 1)
		assert.Equal(t, "MIN", inner.ColumnsPart[1].Expr.Name[0])
		assert.Equal(t, "1.2300", inner.HavingPart[0].Args[1].Literal.Text)
		assert.Len(t, crop.ColumnsPart, 1)
		assert.Equal(t, "name", inner.ColumnsPart[1].Alias)
		assert.Equal(t, qm.ExprCall, inner.HavingPart[0].Args[0].Kind)
	})
	t.Run("count distinct and implicit count having", func(t *testing.T) {
		query, err := BuildResourceDataQuery(&interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Aggr: "count_distinct", Property: "id"}}, "orders")
		require.NoError(t, err)
		assert.True(t, query.ColumnsPart[0].Expr.Distinct)
		query, err = BuildResourceDataQuery(&interfaces.ResourceDataQueryParams{GroupBy: []*interfaces.GroupByItem{{Property: "id"}}, Having: &interfaces.HavingClause{Field: "count(*)", Operation: ">", Value: 1}}, "orders")
		require.NoError(t, err)
		parts := query
		assert.Equal(t, qm.ExprWildcard, parts.ColumnsPart[1].Expr.Args[0].Kind)
	})
	t.Run("cursor request stays in existing paging contract", func(t *testing.T) {
		params := &interfaces.ResourceDataQueryParams{Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 5}}
		query, err := BuildResourceDataQuery(params, "orders")
		require.NoError(t, err)
		assert.IsType(t, &qm.Scan{}, query.FromPart)
		assert.Equal(t, interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 5}, params.Paging)
	})
	cases := map[string]*interfaces.ResourceDataQueryParams{
		"nil request":              nil,
		"continuation":             {Paging: interfaces.PagingRequest{Cursor: "opaque"}},
		"internal conditions":      {FilterCondCfg: &interfaces.FilterCondCfg{Operation: "null", Name: "id"}},
		"internal search position": {SearchAfter: []any{1}},
		"unknown mode":             {Paging: interfaces.PagingRequest{Mode: "bad"}},
		"negative offset":          {Paging: interfaces.PagingRequest{Offset: -1}},
		"invalid limit":            {Paging: interfaces.PagingRequest{Limit: interfaces.MaxPageLimit + 1}},
		"duplicate output":         {OutputFields: []string{"id", "id"}},
		"nil group":                {GroupBy: []*interfaces.GroupByItem{nil}},
		"nil sort":                 {Sort: []*interfaces.SortField{nil}},
		"calendar pending":         {GroupBy: []*interfaces.GroupByItem{{Property: "date", CalendarInterval: "month"}}},
		"invalid aggregate":        {Aggregation: &interfaces.Aggregation{Aggr: "external", Property: "id"}},
		"duplicate group alias":    {Aggregation: &interfaces.Aggregation{Aggr: "min", Property: "amount", Alias: "id"}, GroupBy: []*interfaces.GroupByItem{{Property: "id"}}},
		"having scalar null":       {Aggregation: &interfaces.Aggregation{Aggr: "count", Property: "*"}, Having: &interfaces.HavingClause{Field: "__value", Operation: ">", Value: nil}},
		"having shape":             {Aggregation: &interfaces.Aggregation{Aggr: "count", Property: "*"}, Having: &interfaces.HavingClause{Field: "__value", Operation: ">", Value: []any{1}}},
		"having empty set":         {Aggregation: &interfaces.Aggregation{Aggr: "count", Property: "*"}, Having: &interfaces.HavingClause{Field: "__value", Operation: "in", Value: []any{}}},
		"having source name":       {Aggregation: &interfaces.Aggregation{Aggr: "count", Property: "*"}, Having: &interfaces.HavingClause{Field: "id", Operation: ">", Value: 1}},
		"wrong count having":       {Aggregation: &interfaces.Aggregation{Aggr: "min", Property: "amount"}, Having: &interfaces.HavingClause{Field: "count(*)", Operation: ">", Value: 1}},
		"having like":              {Aggregation: &interfaces.Aggregation{Aggr: "min", Property: "name"}, Having: &interfaces.HavingClause{Field: "__value", Operation: "like", Value: "x"}},
	}
	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			query, err := BuildResourceDataQuery(params, "orders")
			require.Error(t, err)
			assert.Nil(t, query)
		})
	}
}
