// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authz

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"gorm.io/gorm"
)

func countRegistryQueries(t *testing.T, db *gorm.DB) *atomic.Int64 {
	t.Helper()
	count := &atomic.Int64{}
	const name = "test:static-registry-queries"
	if err := db.Callback().Query().Before("gorm:query").Register(name, func(tx *gorm.DB) {
		if tx.Statement.Table == "operations" || tx.Statement.Table == "resource_types" {
			count.Add(1)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Query().Remove(name) })
	return count
}

func TestRegistrySnapshotSharedByRepeatedConcurrentReadsAndTransactions(t *testing.T) {
	en, db := newTestEnforcerDB(t)
	declareCatalogHierarchy(t, db)
	// A diamond with a transitive dependency must be deduplicated deterministically.
	mustNoErr(t, db.Create(&[]model.Operation{
		{ResourceTypeID: "catalog", ID: "left", RequiredOperationIDs: "view_detail"},
		{ResourceTypeID: "catalog", ID: "right", RequiredOperationIDs: "view_detail"},
		{ResourceTypeID: "catalog", ID: "task", RequiredOperationIDs: "left,right"},
	}).Error)
	mustNoErr(t, en.InitializeRegistry(t.Context()))
	count := countRegistryQueries(t, db)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 20 {
				ctx := t.Context()
				requires, err := en.Requirements(ctx, "catalog", []string{"task"})
				if err != nil || !reflect.DeepEqual(requires["task"], []string{"left", "right", "view_detail"}) {
					t.Errorf("requirements=%v, %v", requires, err)
					return
				}
				direct, err := en.DirectRequirements(ctx, "catalog", []string{"task"})
				if err != nil || !reflect.DeepEqual(direct["task"], []string{"left", "right"}) {
					t.Errorf("direct=%v, %v", direct, err)
					return
				}
				reverse, err := en.RequiringOperations(ctx, "catalog", []string{"view_detail"})
				if err != nil || !reflect.DeepEqual(reverse, []string{"view_detail", "left", "right", "task"}) {
					t.Errorf("reverse=%v, %v", reverse, err)
					return
				}
				normalized, err := en.NormalizeOperations(ctx, "catalog", []string{"task"})
				if err != nil || !reflect.DeepEqual(normalized, []string{"task", "left", "right", "view_detail"}) {
					t.Errorf("normalized=%v, %v", normalized, err)
					return
				}
				if err := en.ValidateGrantableOperations(ctx, "catalog", []string{"task"}); err != nil {
					t.Error(err)
					return
				}
				if err := en.ValidateGrantableOperations(ctx, "catalog", []string{"view_summary"}); !errors.Is(err, ErrOperationNotGrantable) {
					t.Errorf("grantable=false: %v", err)
					return
				}
				mapping, err := en.parentOpMap("resource")
				if err != nil || mapping["modify"] != "resource_manage" {
					t.Errorf("parent mapping=%v, %v", mapping, err)
					return
				}
				parent, err := en.parentTypeOf("resource")
				if err != nil || parent != "catalog" {
					t.Errorf("parent=%s, %v", parent, err)
					return
				}
				derived, err := en.derivedRulesByParent(ctx)
				if err != nil || len(derived["catalog"]) != 1 {
					t.Errorf("derivations=%v, %v", derived, err)
					return
				}
				if _, err := en.Registry().Operations(ctx, ""); err != nil {
					t.Error(err)
					return
				}
				if _, err := en.Registry().ResourceTypes(ctx); err != nil {
					t.Error(err)
					return
				}
				if _, err := en.Registry().Operation(ctx, "catalog", "task"); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
	mustNoErr(t, en.InitializeRegistry(t.Context()))
	mustNoErr(t, en.Transaction(t.Context(), func(tx *PolicyTransaction) error {
		if tx.Registry().state != en.Registry().state {
			t.Error("transaction uses a different registry")
		}
		_, err := tx.Requirements(t.Context(), "catalog", []string{"task"})
		if err != nil {
			return err
		}
		_, err = tx.RequiringOperationsByRequirement(t.Context(), "catalog", []string{"view_detail"})
		if err != nil {
			return err
		}
		_, err = tx.DirectRequirements(t.Context(), "catalog", []string{"task"})
		if err != nil {
			return err
		}
		return tx.GrantObjectPermission("reader", "catalog", "cat-1", "view_detail")
	}))
	if got := count.Load(); got != 0 {
		t.Fatalf("static registry queries after initialization = %d, want 0", got)
	}
}

func TestRegistrySnapshotCopiesAndRestartRefresh(t *testing.T) {
	en, db := newTestEnforcerDB(t)
	declareCatalogHierarchy(t, db)
	mustNoErr(t, en.InitializeRegistry(t.Context()))
	ctx := t.Context()
	ops, err := en.Registry().Operations(ctx, "catalog")
	mustNoErr(t, err)
	for i := range ops {
		if ops[i].ID == "view_summary" {
			*ops[i].Grantable = true
			ops[i].ID = "changed"
		}
	}
	op, err := en.Registry().Operation(ctx, "catalog", "view_summary")
	mustNoErr(t, err)
	*op.Grantable = true
	op, err = en.Registry().Operation(ctx, "catalog", "view_summary")
	mustNoErr(t, err)
	if op.IsGrantable() {
		t.Fatal("caller modified cached grantability")
	}
	types, err := en.Registry().ResourceTypes(ctx)
	mustNoErr(t, err)
	types[0].ID = "changed"
	if _, err := en.Registry().ResourceType(ctx, "changed"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("caller modified cached types: %v", err)
	}
	mapping, err := en.parentOpMap("resource")
	mustNoErr(t, err)
	mapping["modify"] = "changed"
	mapping, err = en.parentOpMap("resource")
	mustNoErr(t, err)
	if mapping["modify"] != "resource_manage" {
		t.Fatal("caller modified parent mapping")
	}
	mustNoErr(t, db.Model(&model.ResourceType{}).Where("id = ?", "catalog").Update("name", "new name").Error)
	mustNoErr(t, en.InitializeRegistry(ctx))
	rt, err := en.Registry().ResourceType(ctx, "catalog")
	mustNoErr(t, err)
	if rt.Name == "new name" {
		t.Fatal("reinitialization refreshed a live snapshot")
	}
	restarted, err := New(db)
	mustNoErr(t, err)
	mustNoErr(t, restarted.InitializeRegistry(ctx))
	rt, err = restarted.Registry().ResourceType(ctx, "catalog")
	mustNoErr(t, err)
	if rt.Name != "new name" {
		t.Fatalf("restart did not load persisted metadata: %+v", rt)
	}
}

func TestRegistrySnapshotKeepsPermissionsAndConcreteParentsDynamic(t *testing.T) {
	en, db := newTestEnforcerDB(t)
	declareCatalogHierarchy(t, db)
	ownedBy(t, db, "res-1", "cat-1")
	mustNoErr(t, en.InitializeRegistry(t.Context()))
	count := countRegistryQueries(t, db)
	mustNoErr(t, en.GrantObjectPermission("reader", "catalog", "cat-1", "view_detail"))
	check := func(want bool) {
		t.Helper()
		got, err := en.Check("reader", "resource", "res-1", "view_detail")
		if err != nil || got != want {
			t.Fatalf("check=%v,%v; want %v", got, err, want)
		}
	}
	check(true)
	mustNoErr(t, db.Model(&model.ResourceParent{}).Where("resource_type_id = ? AND resource_id = ?", "resource", "res-1").Update("parent_id", "cat-2").Error)
	check(false)
	mustNoErr(t, en.GrantObjectPermission("reader", "catalog", "cat-2", "view_detail"))
	check(true)
	mustNoErr(t, en.DenyObjectPermission("reader", "resource", "res-1", "view_detail"))
	check(false)
	if count.Load() != 0 {
		t.Fatalf("dynamic decisions queried static registry %d times", count.Load())
	}
}

func TestRegistryInitializationFailureBlocksReadsUntilExplicitRetry(t *testing.T) {
	en, db := newTestEnforcerDB(t)
	declareCatalogHierarchy(t, db)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := en.InitializeRegistry(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("initialization=%v", err)
	}
	count := countRegistryQueries(t, db)
	if _, err := en.Registry().Operations(t.Context(), ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("failed initialization fell back to database: %v", err)
	}
	if count.Load() != 0 {
		t.Fatal("read after failure queried database")
	}
	mustNoErr(t, en.InitializeRegistry(t.Context()))
	if _, err := en.Registry().Operations(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryRejectsInvalidPersistedMetadata(t *testing.T) {
	for _, tc := range []struct {
		name  string
		types []model.ResourceType
		ops   []model.Operation
	}{
		{name: "empty"},
		{name: "unknown type", types: []model.ResourceType{{ID: "root"}}, ops: []model.Operation{{ResourceTypeID: "missing", ID: "view"}}},
		{name: "unknown requirement", types: []model.ResourceType{{ID: "root"}}, ops: []model.Operation{{ResourceTypeID: "root", ID: "manage", RequiredOperationIDs: "missing"}}},
		{name: "self dependency", types: []model.ResourceType{{ID: "root"}}, ops: []model.Operation{{ResourceTypeID: "root", ID: "view", RequiredOperationIDs: "view"}}},
		{name: "dependency cycle", types: []model.ResourceType{{ID: "root"}}, ops: []model.Operation{{ResourceTypeID: "root", ID: "a", RequiredOperationIDs: "b"}, {ResourceTypeID: "root", ID: "b", RequiredOperationIDs: "a"}}},
		{name: "unknown parent", types: []model.ResourceType{{ID: "child", ParentTypeID: "missing"}}},
		{name: "parent cycle", types: []model.ResourceType{{ID: "a", ParentTypeID: "b"}, {ID: "b", ParentTypeID: "a"}}},
		{name: "unknown parent operation", types: []model.ResourceType{{ID: "parent"}, {ID: "child", ParentTypeID: "parent"}}, ops: []model.Operation{{ResourceTypeID: "child", ID: "view", ParentOperationID: "missing"}}},
		{name: "derived target grantable", types: []model.ResourceType{{ID: "parent"}, {ID: "child", ParentTypeID: "parent"}}, ops: []model.Operation{{ResourceTypeID: "parent", ID: "view"}, {ResourceTypeID: "child", ID: "view", DerivedToOperationID: "view"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			en, db := newTestEnforcerDB(t)
			if len(tc.types) > 0 {
				mustNoErr(t, db.Create(&tc.types).Error)
			}
			if len(tc.ops) > 0 {
				mustNoErr(t, db.Create(&tc.ops).Error)
			}
			if err := en.InitializeRegistry(t.Context()); err == nil {
				t.Fatal("accepted invalid registry")
			}
			if en.Registry().state.snapshot != nil {
				t.Fatal("published partial snapshot")
			}
			if _, err := en.Registry().ResourceTypes(t.Context()); err == nil {
				t.Fatal("invalid registry remained readable")
			}
		})
	}
}

func TestRegistryConcurrentInitializationReadsEachStaticTableOnceWithoutWrites(t *testing.T) {
	en, db := newTestEnforcerDB(t)
	declareCatalogHierarchy(t, db)
	count := countRegistryQueries(t, db)
	const callback = "test:registry-load-no-writes"
	onWrite := func(*gorm.DB) { t.Error("snapshot initialization wrote to database") }
	mustNoErr(t, db.Callback().Create().Before("gorm:create").Register(callback, onWrite))
	mustNoErr(t, db.Callback().Update().Before("gorm:update").Register(callback, onWrite))
	mustNoErr(t, db.Callback().Delete().Before("gorm:delete").Register(callback, onWrite))
	t.Cleanup(func() {
		_ = db.Callback().Create().Remove(callback)
		_ = db.Callback().Update().Remove(callback)
		_ = db.Callback().Delete().Remove(callback)
	})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := en.InitializeRegistry(t.Context()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if got := count.Load(); got != 2 {
		t.Fatalf("startup queried static tables %d times, want one query per table", got)
	}
}
