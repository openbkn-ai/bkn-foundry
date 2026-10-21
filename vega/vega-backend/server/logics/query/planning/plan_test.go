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

type targetResolverFunc func(context.Context, qm.BoundScan, qm.ExecutionIdentity, qm.ExecutionOptions) ([]qm.TargetBinding, error)

func (f targetResolverFunc) ResolveSQLQueryTargets(ctx context.Context, scan qm.BoundScan, identity qm.ExecutionIdentity, options qm.ExecutionOptions) ([]qm.TargetBinding, error) {
	return f(ctx, scan, identity, options)
}

func planningScan(instance qm.RelationInstanceID, resourceID string) *qm.BoundScan {
	field := qm.FieldID(instance)
	return &qm.BoundScan{
		BoundRelationBase: qm.BoundRelationBase{OutputSchema: qm.Schema{{ID: field, Name: "id", Type: qm.LogicalType{Kind: qm.TypeInteger, BitWidth: 64}}}},
		Resource:          qm.ResourceReference{ID: resourceID, CatalogID: "catalog", Category: "table"},
		Instance:          instance,
		Fields:            []qm.BoundSourceField{{ID: field, LogicalName: "id"}},
	}
}

func planningTarget(scan qm.BoundScan, key string, source qm.QuerySource) qm.TargetBinding {
	return qm.TargetBinding{
		Target: qm.ExecutionTarget{CatalogID: scan.Resource.CatalogID, ConnectorType: "mysql", PhysicalTargetKey: key,
			ConnectionProfile: "profile", EffectiveSourceIdentity: "principal", AuthorizationNamespace: "namespace",
			SchemaRevision: "schema-v1", ConfigRevision: "config-v1"},
		Source:    source,
		Relations: []qm.RelationBinding{{Instance: scan.Instance, ResourceID: scan.Resource.ID, Identifier: qm.PhysicalIdentifier{"db", scan.Resource.ID}}},
		Fields:    []qm.FieldBinding{{Address: qm.FieldAddress{Relation: scan.Instance, Field: scan.Fields[0].ID}, Identifier: qm.PhysicalIdentifier{"id"}}},
	}
}

func TestPlan(t *testing.T) {
	t.Run("self join preserves instances and complete query", func(t *testing.T) {
		left, right := planningScan(1, "orders"), planningScan(2, "orders")
		join := &qm.BoundJoin{Left: left, Right: right, Kind: qm.JoinLeft}
		query := &qm.BoundQuery{Root: &qm.BoundLimit{Input: &qm.BoundProject{Input: join}, Count: 10}}
		identity := qm.ExecutionIdentity{SubjectID: "caller"}
		calls := 0
		var candidates []qm.TargetBinding
		resolver := targetResolverFunc(func(_ context.Context, scan qm.BoundScan, got qm.ExecutionIdentity, options qm.ExecutionOptions) ([]qm.TargetBinding, error) {
			calls++
			assert.Equal(t, identity, got)
			assert.Equal(t, qm.QuerySourceSource, options.QuerySource)
			candidate := planningTarget(scan, "db", qm.QuerySourceSource)
			candidates = append(candidates, candidate)
			return []qm.TargetBinding{candidate}, nil
		})
		plan, err := Plan(context.Background(), query, identity, qm.ExecutionOptions{}, resolver)
		require.NoError(t, err)
		assert.Same(t, query, plan.Query)
		assert.Equal(t, 2, calls)
		assert.Len(t, plan.TargetBinding.Relations, 2)
		assert.Len(t, plan.TargetBinding.Fields, 2)
		assert.NotEqual(t, plan.TargetBinding.Relations[0].Instance, plan.TargetBinding.Relations[1].Instance)
		require.NotEmpty(t, plan.TargetBinding.Fingerprint)
		candidates[0].Relations[0].Identifier[0] = "changed"
		candidates[0].Fields[0].Identifier[0] = "changed"
		assert.Equal(t, "db", plan.TargetBinding.Relations[0].Identifier[0])
		assert.Equal(t, "id", plan.TargetBinding.Fields[0].Identifier[0])
		again, err := Plan(context.Background(), query, identity, qm.ExecutionOptions{}, resolver)
		require.NoError(t, err)
		assert.Equal(t, plan.TargetBinding.Fingerprint, again.TargetBinding.Fingerprint)
	})
	t.Run("intersection considers alternatives without splitting query", func(t *testing.T) {
		query := &qm.BoundQuery{Root: &qm.BoundJoin{Left: planningScan(1, "a"), Right: planningScan(2, "b"), Kind: qm.JoinInner}}
		resolver := targetResolverFunc(func(_ context.Context, scan qm.BoundScan, _ qm.ExecutionIdentity, _ qm.ExecutionOptions) ([]qm.TargetBinding, error) {
			if scan.Instance == 1 {
				return []qm.TargetBinding{planningTarget(scan, "first", qm.QuerySourceSource), planningTarget(scan, "shared", qm.QuerySourceSource)}, nil
			}
			return []qm.TargetBinding{planningTarget(scan, "shared", qm.QuerySourceSource)}, nil
		})
		plan, err := Plan(context.Background(), query, qm.ExecutionIdentity{}, qm.ExecutionOptions{}, resolver)
		require.NoError(t, err)
		assert.Equal(t, "shared", plan.TargetBinding.Target.PhysicalTargetKey)
	})
	t.Run("every execution environment component matters", func(t *testing.T) {
		changes := map[string]func(*qm.TargetBinding){
			"physical target":  func(c *qm.TargetBinding) { c.Target.PhysicalTargetKey = "different" },
			"connector":        func(c *qm.TargetBinding) { c.Target.ConnectorType = "different" },
			"connection":       func(c *qm.TargetBinding) { c.Target.ConnectionProfile = "different" },
			"identity":         func(c *qm.TargetBinding) { c.Target.EffectiveSourceIdentity = "different" },
			"authorization":    func(c *qm.TargetBinding) { c.Target.AuthorizationNamespace = "different" },
			"schema revision":  func(c *qm.TargetBinding) { c.Target.SchemaRevision = "different" },
			"config revision":  func(c *qm.TargetBinding) { c.Target.ConfigRevision = "different" },
			"source and index": func(c *qm.TargetBinding) { c.Source = qm.QuerySourceIndex },
			"freshness":        func(c *qm.TargetBinding) { c.Freshness.Revision = "different" },
		}
		for name, change := range changes {
			t.Run(name, func(t *testing.T) {
				query := &qm.BoundQuery{Root: &qm.BoundJoin{Left: planningScan(1, "a"), Right: planningScan(2, "b"), Kind: qm.JoinInner}}
				resolver := targetResolverFunc(func(_ context.Context, s qm.BoundScan, _ qm.ExecutionIdentity, _ qm.ExecutionOptions) ([]qm.TargetBinding, error) {
					candidate := planningTarget(s, "db", qm.QuerySourceSource)
					if s.Instance == 2 {
						change(&candidate)
					}
					return []qm.TargetBinding{candidate}, nil
				})
				plan, err := Plan(context.Background(), query, qm.ExecutionIdentity{}, qm.ExecutionOptions{QuerySource: qm.QuerySourceAuto}, resolver)
				require.Error(t, err)
				assert.Nil(t, plan)
				var e *qm.CompileError
				require.ErrorAs(t, err, &e)
				assert.Equal(t, qm.ErrorPlanningUnsupported, e.Kind)
			})
		}
	})
	t.Run("different catalogs rejected before resolving targets", func(t *testing.T) {
		right := planningScan(2, "b")
		right.Resource.CatalogID = "other"
		query := &qm.BoundQuery{Root: &qm.BoundJoin{Left: planningScan(1, "a"), Right: right, Kind: qm.JoinInner}}
		calls := 0
		resolver := targetResolverFunc(func(context.Context, qm.BoundScan, qm.ExecutionIdentity, qm.ExecutionOptions) ([]qm.TargetBinding, error) {
			calls++
			return nil, nil
		})
		plan, err := Plan(context.Background(), query, qm.ExecutionIdentity{}, qm.ExecutionOptions{}, resolver)
		require.Error(t, err)
		assert.Nil(t, plan)
		assert.Zero(t, calls)
	})
	t.Run("source routing never silently picks index", func(t *testing.T) {
		query := &qm.BoundQuery{Root: planningScan(1, "a")}
		resolver := targetResolverFunc(func(_ context.Context, s qm.BoundScan, _ qm.ExecutionIdentity, _ qm.ExecutionOptions) ([]qm.TargetBinding, error) {
			return []qm.TargetBinding{planningTarget(s, "index", qm.QuerySourceIndex)}, nil
		})
		plan, err := Plan(context.Background(), query, qm.ExecutionIdentity{}, qm.ExecutionOptions{}, resolver)
		require.Error(t, err)
		assert.Nil(t, plan)
		plan, err = Plan(context.Background(), query, qm.ExecutionIdentity{}, qm.ExecutionOptions{QuerySource: qm.QuerySourceIndex}, resolver)
		require.NoError(t, err)
		assert.Equal(t, qm.QuerySourceIndex, plan.TargetBinding.Source)
		plan, err = Plan(context.Background(), query, qm.ExecutionIdentity{}, qm.ExecutionOptions{QuerySource: qm.QuerySourceIndex, IgnoreLocalIndex: true}, resolver)
		require.Error(t, err)
		assert.Nil(t, plan)
	})
	t.Run("malformed mapping cannot become executable plan", func(t *testing.T) {
		changes := map[string]func(*qm.TargetBinding){
			"missing target identity": func(c *qm.TargetBinding) { c.Target.EffectiveSourceIdentity = "" },
			"missing relation":        func(c *qm.TargetBinding) { c.Relations = nil },
			"wrong instance":          func(c *qm.TargetBinding) { c.Relations[0].Instance = 9 },
			"wrong resource":          func(c *qm.TargetBinding) { c.Relations[0].ResourceID = "other" },
			"wrong catalog":           func(c *qm.TargetBinding) { c.Target.CatalogID = "other" },
			"empty identifier":        func(c *qm.TargetBinding) { c.Relations[0].Identifier = qm.PhysicalIdentifier{"db", ""} },
			"missing field":           func(c *qm.TargetBinding) { c.Fields = nil },
			"wrong field":             func(c *qm.TargetBinding) { c.Fields[0].Address.Field = 9 },
			"duplicate field":         func(c *qm.TargetBinding) { c.Fields = append(c.Fields, c.Fields[0]) },
			"bad missing policy":      func(c *qm.TargetBinding) { c.Fields[0].MissingValue = "bad" },
		}
		for name, change := range changes {
			t.Run(name, func(t *testing.T) {
				resolver := targetResolverFunc(func(_ context.Context, s qm.BoundScan, _ qm.ExecutionIdentity, _ qm.ExecutionOptions) ([]qm.TargetBinding, error) {
					c := planningTarget(s, "db", qm.QuerySourceSource)
					change(&c)
					return []qm.TargetBinding{c}, nil
				})
				plan, err := Plan(context.Background(), &qm.BoundQuery{Root: planningScan(1, "a")}, qm.ExecutionIdentity{}, qm.ExecutionOptions{}, resolver)
				require.Error(t, err)
				assert.Nil(t, plan)
			})
		}
	})
	t.Run("every relation wrapper is traversed", func(t *testing.T) {
		wrappers := []func(qm.BoundRelation) qm.BoundRelation{
			func(r qm.BoundRelation) qm.BoundRelation { return &qm.BoundFilter{Input: r} },
			func(r qm.BoundRelation) qm.BoundRelation { return &qm.BoundAggregate{Input: r} },
			func(r qm.BoundRelation) qm.BoundRelation { return &qm.BoundProject{Input: r} },
			func(r qm.BoundRelation) qm.BoundRelation { return &qm.BoundDistinct{Input: r} },
			func(r qm.BoundRelation) qm.BoundRelation { return &qm.BoundSort{Input: r} },
			func(r qm.BoundRelation) qm.BoundRelation { return &qm.BoundLimit{Input: r} },
			func(r qm.BoundRelation) qm.BoundRelation { return &qm.BoundSubquery{Input: r} },
			func(r qm.BoundRelation) qm.BoundRelation { return &qm.BoundViewBoundary{Input: r} },
			func(r qm.BoundRelation) qm.BoundRelation { return &qm.BoundSearch{Input: r} },
		}
		var root qm.BoundRelation = planningScan(1, "a")
		for _, wrap := range wrappers {
			root = wrap(root)
		}
		calls := 0
		resolver := targetResolverFunc(func(_ context.Context, s qm.BoundScan, _ qm.ExecutionIdentity, _ qm.ExecutionOptions) ([]qm.TargetBinding, error) {
			calls++
			return []qm.TargetBinding{planningTarget(s, "db", qm.QuerySourceSource)}, nil
		})
		_, err := Plan(context.Background(), &qm.BoundQuery{Root: root}, qm.ExecutionIdentity{}, qm.ExecutionOptions{}, resolver)
		require.NoError(t, err)
		assert.Equal(t, 1, calls)
	})
	t.Run("invalid trees and canceled context do not call resolver", func(t *testing.T) {
		cyclic := &qm.BoundFilter{}
		cyclic.Input = cyclic
		deep := qm.BoundRelation(planningScan(1, "a"))
		for i := 0; i < qm.MaxDepth; i++ {
			deep = &qm.BoundLimit{Input: deep}
		}
		calls := 0
		resolver := targetResolverFunc(func(context.Context, qm.BoundScan, qm.ExecutionIdentity, qm.ExecutionOptions) ([]qm.TargetBinding, error) {
			calls++
			return nil, errors.New("unexpected resolution")
		})
		var typedNil *qm.BoundScan
		for _, q := range []*qm.BoundQuery{nil, {}, {Root: typedNil}, {Root: cyclic}, {Root: deep}, {Root: &qm.BoundJoin{Left: planningScan(1, "a"), Right: planningScan(1, "a")}}} {
			_, err := Plan(context.Background(), q, qm.ExecutionIdentity{}, qm.ExecutionOptions{}, resolver)
			require.Error(t, err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := Plan(ctx, &qm.BoundQuery{Root: planningScan(1, "a")}, qm.ExecutionIdentity{}, qm.ExecutionOptions{}, resolver)
		require.ErrorIs(t, err, context.Canceled)
		assert.Zero(t, calls)
	})
	t.Run("cancellation during metadata lookup returns no plan", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		resolver := targetResolverFunc(func(_ context.Context, s qm.BoundScan, _ qm.ExecutionIdentity, _ qm.ExecutionOptions) ([]qm.TargetBinding, error) {
			cancel()
			return []qm.TargetBinding{planningTarget(s, "db", qm.QuerySourceSource)}, nil
		})
		plan, err := Plan(ctx, &qm.BoundQuery{Root: planningScan(1, "a")}, qm.ExecutionIdentity{}, qm.ExecutionOptions{}, resolver)
		require.ErrorIs(t, err, context.Canceled)
		assert.Nil(t, plan)
	})
	t.Run("unbounded candidate lists rejected", func(t *testing.T) {
		resolver := targetResolverFunc(func(context.Context, qm.BoundScan, qm.ExecutionIdentity, qm.ExecutionOptions) ([]qm.TargetBinding, error) {
			return make([]qm.TargetBinding, qm.MaxNodes+1), nil
		})
		plan, err := Plan(context.Background(), &qm.BoundQuery{Root: planningScan(1, "a")}, qm.ExecutionIdentity{}, qm.ExecutionOptions{}, resolver)
		require.Error(t, err)
		assert.Nil(t, plan)
		var e *qm.CompileError
		require.ErrorAs(t, err, &e)
		assert.Equal(t, qm.ErrorLimit, e.Kind)
	})
	t.Run("resolver failure propagated unchanged", func(t *testing.T) {
		failure := errors.New("resolver unavailable")
		resolver := targetResolverFunc(func(context.Context, qm.BoundScan, qm.ExecutionIdentity, qm.ExecutionOptions) ([]qm.TargetBinding, error) {
			return nil, failure
		})
		_, err := Plan(context.Background(), &qm.BoundQuery{Root: planningScan(1, "a")}, qm.ExecutionIdentity{}, qm.ExecutionOptions{}, resolver)
		require.ErrorIs(t, err, failure)
	})
}
