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
		query, options, err := BuildResourceDataQuery(params, "orders")
		require.NoError(t, err)
		assert.Equal(t, int64(40), options.Paging.Offset)
		assert.Equal(t, int64(20), options.Paging.Limit)
		assert.True(t, options.NeedTotal)
		assert.Equal(t, qm.QuerySourceSource, options.QuerySource)
		assert.Equal(t, qm.BinaryMetadata, options.BinaryMode)
		outer := query.Root.(*qm.Project)
		inner := outer.Input.(*qm.Project)
		scan := inner.Input.(*qm.Scan)
		assert.Equal(t, "orders", scan.ResourceID)
		assert.Equal(t, qm.ExprWildcard, inner.Columns[0].Expr.Kind)
		assert.Equal(t, "output_fields[0]", outer.Columns[0].Origin.Path)
		assert.Empty(t, params.Paging.Mode)
		params.OutputFields[0] = "changed"
		assert.Equal(t, []string{"id"}, outer.Columns[0].Expr.Name)
	})
	t.Run("aggregate aliases and count remain inside output cropping", func(t *testing.T) {
		params := &interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Aggr: "min", Property: "amount", Alias: "name"}, GroupBy: []*interfaces.GroupByItem{{Property: "id"}}, Having: &interfaces.HavingClause{Field: "__value", Operation: ">", Value: json.Number("1.2300")}, Sort: []*interfaces.SortField{{Field: "name", Direction: "desc"}}, OutputFields: []string{"name"}}
		query, _, err := BuildResourceDataQuery(params, "orders")
		require.NoError(t, err)
		crop := query.Root.(*qm.Project)
		sort := crop.Input.(*qm.Sort)
		project := sort.Input.(*qm.Project)
		having := project.Input.(*qm.Filter)
		aggregate := having.Input.(*qm.Aggregate)
		assert.Len(t, project.Columns, 2)
		assert.Len(t, aggregate.GroupKeys, 1)
		assert.Equal(t, "MIN", aggregate.Calls[0].Name[0])
		assert.True(t, having.Having)
		assert.Equal(t, qm.ExprAggregateRef, having.Predicate.Args[0].Kind)
		assert.Equal(t, "1.2300", having.Predicate.Args[1].Literal.Text)
		assert.Len(t, crop.Columns, 1)
		assert.Equal(t, "name", project.Columns[1].Alias)
	})
	t.Run("count distinct and implicit count having", func(t *testing.T) {
		query, _, err := BuildResourceDataQuery(&interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Aggr: "count_distinct", Property: "id"}}, "orders")
		require.NoError(t, err)
		assert.True(t, query.Root.(*qm.Project).Input.(*qm.Aggregate).Calls[0].Distinct)
		query, _, err = BuildResourceDataQuery(&interfaces.ResourceDataQueryParams{GroupBy: []*interfaces.GroupByItem{{Property: "id"}}, Having: &interfaces.HavingClause{Field: "count(*)", Operation: ">", Value: 1}}, "orders")
		require.NoError(t, err)
		aggregate := query.Root.(*qm.Project).Input.(*qm.Filter).Input.(*qm.Aggregate)
		assert.Equal(t, qm.ExprWildcard, aggregate.Calls[0].Args[0].Kind)
	})
	t.Run("cursor options apply existing defaults", func(t *testing.T) {
		_, options, err := BuildResourceDataQuery(&interfaces.ResourceDataQueryParams{Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 5}}, "orders")
		require.NoError(t, err)
		assert.Equal(t, qm.PagingCursor, options.Paging.Mode)
		assert.Equal(t, time.Duration(interfaces.DefaultCursorKeepAliveSec)*time.Second, options.Paging.KeepAlive)
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
			query, _, err := BuildResourceDataQuery(params, "orders")
			require.Error(t, err)
			assert.Nil(t, query)
		})
	}
}
