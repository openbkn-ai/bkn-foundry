// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package planning

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	qm "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
)

type sourceResolverFunc func(context.Context, qm.Scan, qm.ExecutionIdentity) (qm.Scan, error)

func (f sourceResolverFunc) ResolveSQLQuerySource(ctx context.Context, scan qm.Scan, identity qm.ExecutionIdentity) (qm.Scan, error) {
	return f(ctx, scan, identity)
}

func planningScan(instance qm.RelationInstanceID, resourceID string) *qm.Scan {
	field := qm.FieldID(instance)
	return &qm.Scan{
		OutputSchema: qm.Schema{{ID: field, Name: "id", Type: qm.LogicalType{Kind: qm.TypeInteger, BitWidth: 64}}},
		Resource:     qm.ResourceReference{ID: resourceID, CatalogID: "catalog", Category: "table"},
		Instance:     instance,
		Fields:       []qm.SourceField{{ID: field, LogicalName: "id"}},
	}
}

func planningSource(scan qm.Scan) qm.Scan {
	scan.Identifier = qm.PhysicalIdentifier{"db", scan.Resource.ID}
	scan.Fields = append([]qm.SourceField(nil), scan.Fields...)
	for i := range scan.Fields {
		scan.Fields[i].Identifier = qm.PhysicalIdentifier{scan.Fields[i].LogicalName}
	}
	return scan
}

func TestPlan(t *testing.T) {
	t.Run("self join preserves instances and complete query", func(t *testing.T) {
		left, right := planningScan(1, "orders"), planningScan(2, "orders")
		join := &qm.Join{Left: left, Right: right, Kind: qm.JoinLeft}
		query := qm.Select().From(join).Limit(10, 0)
		identity := qm.ExecutionIdentity{SubjectID: "caller"}
		calls := 0
		var candidates []qm.Scan
		resolver := sourceResolverFunc(func(_ context.Context, scan qm.Scan, got qm.ExecutionIdentity) (qm.Scan, error) {
			calls++
			assert.Equal(t, identity, got)
			candidate := planningSource(scan)
			candidates = append(candidates, candidate)
			return candidate, nil
		})
		plan, err := Plan(context.Background(), query, identity, resolver)
		require.NoError(t, err)
		assert.NotSame(t, query, plan)
		assert.NotSame(t, query.FromPart, plan.FromPart)
		assert.Empty(t, left.Identifier)
		assert.Empty(t, right.Fields[0].Identifier)
		assert.Equal(t, 2, calls)
		gotJoin := plan.FromPart.(*qm.Join)
		gotLeft, gotRight := gotJoin.Left.(*qm.Scan), gotJoin.Right.(*qm.Scan)
		assert.Len(t, gotLeft.Fields, 1)
		assert.Len(t, gotRight.Fields, 1)
		assert.NotEqual(t, gotLeft.Instance, gotRight.Instance)
		candidates[0].Identifier[0] = "changed"
		candidates[0].Fields[0].Identifier[0] = "changed"
		assert.Equal(t, "db", gotLeft.Identifier[0])
		assert.Equal(t, "id", gotLeft.Fields[0].Identifier[0])
		again, err := Plan(context.Background(), query, identity, resolver)
		require.NoError(t, err)
		assert.Equal(t, plan, again)
		assert.Equal(t, query.LimitPart, plan.LimitPart)
	})
	t.Run("different catalogs rejected before resolving targets", func(t *testing.T) {
		right := planningScan(2, "b")
		right.Resource.CatalogID = "other"
		query := qm.Select().From(&qm.Join{Left: planningScan(1, "a"), Right: right, Kind: qm.JoinInner})
		calls := 0
		resolver := sourceResolverFunc(func(context.Context, qm.Scan, qm.ExecutionIdentity) (qm.Scan, error) {
			calls++
			return qm.Scan{}, nil
		})
		plan, err := Plan(context.Background(), query, qm.ExecutionIdentity{}, resolver)
		require.Error(t, err)
		assert.Nil(t, plan)
		assert.Zero(t, calls)
	})
	t.Run("malformed mapping cannot become executable plan", func(t *testing.T) {
		changes := map[string]func(*qm.Scan){
			"missing relation":   func(c *qm.Scan) { c.Identifier = nil },
			"wrong instance":     func(c *qm.Scan) { c.Instance = 9 },
			"empty identifier":   func(c *qm.Scan) { c.Identifier = qm.PhysicalIdentifier{"db", ""} },
			"missing field":      func(c *qm.Scan) { c.Fields = nil },
			"wrong field":        func(c *qm.Scan) { c.Fields[0].ID = 9 },
			"duplicate field":    func(c *qm.Scan) { c.Fields = append(c.Fields, c.Fields[0]) },
			"wrong resource":     func(c *qm.Scan) { c.Resource.ID = "other" },
			"wrong native type":  func(c *qm.Scan) { c.Fields[0].NativeType.Name = "other" },
			"wrong logical name": func(c *qm.Scan) { c.Fields[0].LogicalName = "other" },
			"empty column":       func(c *qm.Scan) { c.Fields[0].Identifier = nil },
		}
		for name, change := range changes {
			t.Run(name, func(t *testing.T) {
				resolver := sourceResolverFunc(func(_ context.Context, s qm.Scan, _ qm.ExecutionIdentity) (qm.Scan, error) {
					c := planningSource(s)
					change(&c)
					return c, nil
				})
				plan, err := Plan(context.Background(), qm.Select().From(planningScan(1, "a")), qm.ExecutionIdentity{}, resolver)
				require.Error(t, err)
				assert.Nil(t, plan)
			})
		}
	})
	t.Run("nested select sources are traversed", func(t *testing.T) {
		query := qm.Select().FromSelect(qm.Select().FromSelect(qm.Select().From(planningScan(1, "a")), "inner"), "outer")
		calls := 0
		resolver := sourceResolverFunc(func(_ context.Context, s qm.Scan, _ qm.ExecutionIdentity) (qm.Scan, error) {
			calls++
			return planningSource(s), nil
		})
		_, err := Plan(context.Background(), query, qm.ExecutionIdentity{}, resolver)
		require.NoError(t, err)
		assert.Equal(t, 1, calls)
	})
	t.Run("invalid trees and canceled context do not call resolver", func(t *testing.T) {
		cycleSource := &qm.Subquery{Alias: "cycle"}
		cyclic := qm.Select().From(cycleSource)
		cycleSource.Query = cyclic
		deep := qm.Select().From(planningScan(1, "a"))
		for i := 0; i < qm.MaxDepth; i++ {
			deep = qm.Select().FromSelect(deep, "deep")
		}
		calls := 0
		resolver := sourceResolverFunc(func(context.Context, qm.Scan, qm.ExecutionIdentity) (qm.Scan, error) {
			calls++
			return qm.Scan{}, errors.New("unexpected resolution")
		})
		var typedNil *qm.Scan
		for _, q := range []*qm.SelectBuilder{nil, {}, qm.Select().From(typedNil), cyclic, deep, qm.Select().From(&qm.Join{Left: planningScan(1, "a"), Right: planningScan(1, "a")})} {
			_, err := Plan(context.Background(), q, qm.ExecutionIdentity{}, resolver)
			require.Error(t, err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := Plan(ctx, qm.Select().From(planningScan(1, "a")), qm.ExecutionIdentity{}, resolver)
		require.ErrorIs(t, err, context.Canceled)
		assert.Zero(t, calls)
	})
	t.Run("cancellation during metadata lookup returns no plan", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		resolver := sourceResolverFunc(func(_ context.Context, s qm.Scan, _ qm.ExecutionIdentity) (qm.Scan, error) {
			cancel()
			return planningSource(s), nil
		})
		plan, err := Plan(ctx, qm.Select().From(planningScan(1, "a")), qm.ExecutionIdentity{}, resolver)
		require.ErrorIs(t, err, context.Canceled)
		assert.Nil(t, plan)
	})
	t.Run("resolver failure propagated unchanged", func(t *testing.T) {
		failure := errors.New("resolver unavailable")
		resolver := sourceResolverFunc(func(context.Context, qm.Scan, qm.ExecutionIdentity) (qm.Scan, error) {
			return qm.Scan{}, failure
		})
		_, err := Plan(context.Background(), qm.Select().From(planningScan(1, "a")), qm.ExecutionIdentity{}, resolver)
		require.ErrorIs(t, err, failure)
	})
}
