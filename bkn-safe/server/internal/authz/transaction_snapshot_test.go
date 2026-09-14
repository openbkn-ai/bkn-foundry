// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authz

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/casbin/casbin/v2"
	casbinmodel "github.com/casbin/casbin/v2/model"
	gormadapter "github.com/casbin/gorm-adapter/v3"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/database"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

// fileTestDB is a file-backed sqlite store: unlike ":memory:" it gives every
// pooled connection the same database, which concurrent tests need.
func fileTestDB(t testing.TB) *gorm.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "authz.db")
	db, err := gorm.Open(sqlite.Open("file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(30000)"),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func ruleStrings(rows [][]string) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, strings.Join(r, "|"))
	}
	return out
}

func sortedRules(rows [][]string) []string {
	out := ruleStrings(rows)
	sort.Strings(out)
	return out
}

// freshEnforcer loads an independent enforcer from the committed store — what
// the pre-#1511 code held after reloading on every write.
func freshEnforcer(t testing.TB, db *gorm.DB) *Enforcer {
	t.Helper()
	adapter, err := gormadapter.NewAdapterByDB(db)
	if err != nil {
		t.Fatal(err)
	}
	m, err := casbinmodel.NewModelFromString(modelConf)
	if err != nil {
		t.Fatal(err)
	}
	e, err := casbin.NewSyncedEnforcer(m, adapter)
	if err != nil {
		t.Fatal(err)
	}
	return &Enforcer{e: e, db: db}
}

// assertMatchesStore requires the live model to equal a fresh load of the store
// in rules, rule order (listings return that order), bindings and decisions.
func assertMatchesStore(t *testing.T, step string, live *Enforcer, db *gorm.DB, users, ids, ops []string) {
	t.Helper()
	fresh := freshEnforcer(t, db)
	livePolicy, _ := live.e.GetPolicy()
	storePolicy, _ := fresh.e.GetPolicy()
	if !reflect.DeepEqual(ruleStrings(livePolicy), ruleStrings(storePolicy)) {
		t.Fatalf("%s: live p rules differ from the store\nlive =%v\nstore=%v", step, ruleStrings(livePolicy), ruleStrings(storePolicy))
	}
	liveGrouping, _ := live.e.GetGroupingPolicy()
	storeGrouping, _ := fresh.e.GetGroupingPolicy()
	if !reflect.DeepEqual(sortedRules(liveGrouping), sortedRules(storeGrouping)) {
		t.Fatalf("%s: live g rules differ from the store\nlive =%v\nstore=%v", step, sortedRules(liveGrouping), sortedRules(storeGrouping))
	}
	for _, u := range users {
		for _, id := range ids {
			for _, op := range ops {
				got, gotErr := live.OperationDecision(context.Background(), u, "catalog", id, op)
				want, wantErr := fresh.OperationDecision(context.Background(), u, "catalog", id, op)
				if (gotErr == nil) != (wantErr == nil) || got.Decision != want.Decision || got.Basis != want.Basis {
					t.Fatalf("%s: decision for %s catalog:%s %s: live=%+v,%v store=%+v,%v",
						step, u, id, op, got, gotErr, want, wantErr)
				}
			}
		}
	}
}

// TestSnapshotTransactionsMatchStore drives a seeded random mix of every write
// shape and checks after each one that the published model equals a fresh
// load of the store. Transactions start from an in-memory copy and publish it
// on commit (#1511), so this is what guarantees they never drift.
func TestSnapshotTransactionsMatchStore(t *testing.T) {
	db := fileTestDB(t)
	en, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	rng := rand.New(rand.NewSource(1511))
	users := []string{"u-1", "u-2", "u-3", "u-4"}
	roles := []string{"r-a", "r-b"}
	ids := []string{"c1", "c2", "c3", "c4", "c5"}
	ops := []string{"view_detail", "modify", "delete", "query_data", "resource_manage"}
	pick := func(s []string) string { return s[rng.Intn(len(s))] }
	errInjected := errors.New("injected failure")

	for i := 0; i < 400; i++ {
		var step string
		var opErr error
		switch rng.Intn(12) {
		case 0, 1, 2:
			u, id := pick(users), pick(ids)
			set := []string{pick(ops), pick(ops)}
			step = fmt.Sprintf("grant %s %s %v", u, id, set)
			opErr = en.GrantNormalizedObjectPermissions(ctx, u, "catalog", id, set)
		case 3:
			u, id, op := pick(users), pick(ids), pick(ops)
			step = fmt.Sprintf("revoke %s %s %s", u, id, op)
			opErr = en.RevokeObjectPermission(u, "catalog", id, op)
		case 4:
			u, r := pick(users), pick(roles)
			step = fmt.Sprintf("assign %s %s", u, r)
			opErr = en.AssignRole(u, r)
		case 5:
			u, r := pick(users), pick(roles)
			step = fmt.Sprintf("unassign %s %s", u, r)
			opErr = en.RemoveRole(u, r)
		case 6:
			r, op := pick(roles), pick(ops)
			step = fmt.Sprintf("role grant %s %s", r, op)
			opErr = en.GrantRolePermission(r, "catalog", "*", op)
		case 7:
			u, id, op := pick(users), pick(ids), pick(ops)
			step = fmt.Sprintf("deny %s %s %s", u, id, op)
			opErr = en.DenyObjectPermission(u, "catalog", id, op)
		case 8:
			id := pick(ids)
			step = "remove resource " + id
			opErr = en.RemoveResourcePolicies("catalog", id)
		case 9:
			u := pick(users)
			step = "remove accessor " + u
			opErr = en.RemoveAccessor(u)
		case 10:
			r := pick(roles)
			step = "remove role " + r
			opErr = en.RemoveRoleCompletely(r)
		case 11:
			// A write that fails after changing the transactional model must
			// leave both the store and the live model untouched.
			u, id := pick(users), pick(ids)
			step = "failing transaction " + u + " " + id
			opErr = en.Transaction(ctx, func(tx *PolicyTransaction) error {
				if err := tx.GrantObjectPermission(u, "catalog", id, "modify"); err != nil {
					return err
				}
				return errInjected
			})
			if errors.Is(opErr, errInjected) {
				opErr = nil
			}
		}
		if opErr != nil {
			t.Fatalf("step %d %s: %v", i, step, opErr)
		}
		assertMatchesStore(t, fmt.Sprintf("step %d %s", i, step), en, db, append(users, "u-none"), ids, ops)
	}
}

// TestQueuedWriterWithEndedContextDoesNoWork is the #1511 failure mode: a
// caller that gave up while queued must leave without touching the store,
// instead of running a transaction after its client has disconnected.
func TestQueuedWriterWithEndedContextDoesNoWork(t *testing.T) {
	en, err := New(fileTestDB(t))
	if err != nil {
		t.Fatal(err)
	}
	holding, releaseHolder := make(chan struct{}), make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		holderDone <- en.Transaction(context.Background(), func(*PolicyTransaction) error {
			close(holding)
			<-releaseHolder
			return nil
		})
	}()
	<-holding
	var ran atomic.Bool
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	queuedDone := make(chan error, 1)
	go func() {
		queuedDone <- en.Transaction(ctx, func(*PolicyTransaction) error { ran.Store(true); return nil })
	}()
	// A queue that ignores the context would keep this writer waiting on the
	// holder, and the holder is released only after the check below.
	select {
	case err := <-queuedDone:
		if !errors.Is(err, context.DeadlineExceeded) || ran.Load() {
			t.Errorf("queued writer: err=%v ran=%v", err, ran.Load())
		}
	case <-time.After(5 * time.Second):
		t.Error("queued writer did not leave the queue when its context ended")
	}
	// Role bindings written from request handlers follow the same contract.
	bindCtx, cancelBind := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelBind()
	bindDone := make(chan error, 1)
	go func() { bindDone <- en.AssignRoleContext(bindCtx, "u-late", "r-late") }()
	select {
	case err := <-bindDone:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("queued role binding: err=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("queued role binding did not leave the queue when its context ended")
	}
	close(releaseHolder)
	if err := <-holderDone; err != nil {
		t.Fatal(err)
	}
	if roles, _ := en.RolesForAccessor("u-late"); len(roles) != 0 {
		t.Fatalf("abandoned role binding was written: %v", roles)
	}
}

// TestChecksDuringWrites runs checks against a stream of writes (meaningful
// under -race) and requires the end state to match the store.
func TestChecksDuringWrites(t *testing.T) {
	db := fileTestDB(t)
	en, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := en.AssignRole("u-1", "r-a"); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var readers sync.WaitGroup
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_, _ = en.Check("u-1", "catalog", "c-1", "view_detail")
					_, _ = en.AllowedOps("u-2", "catalog", "c-2", []string{"view_detail", "modify"})
				}
			}
		}()
	}
	var writers sync.WaitGroup
	for w := 0; w < 4; w++ {
		writers.Add(1)
		go func(w int) {
			defer writers.Done()
			for i := 0; i < 25; i++ {
				if err := en.GrantNormalizedObjectPermissions(context.Background(), fmt.Sprintf("u-%d", w),
					"catalog", fmt.Sprintf("c-%d", (w*25+i)%10), []string{"view_detail", "modify"}); err != nil {
					t.Error(err)
				}
				if i%5 == 0 {
					_ = en.AssignRole(fmt.Sprintf("u-%d", w), "r-b")
					_ = en.RemoveRole(fmt.Sprintf("u-%d", w), "r-b")
				}
			}
		}(w)
	}
	writers.Wait()
	close(stop)
	readers.Wait()
	assertMatchesStore(t, "after concurrent writes", en, db, []string{"u-0", "u-1", "u-2", "u-3"},
		[]string{"c-0", "c-1", "c-2", "c-5"}, []string{"view_detail", "modify"})
}

// TestWritePathsNeedOneConnection pins the pool to a single connection. The
// pool is capped since #1511; a write path that asked for a second connection
// while holding its transaction would hang here instead of in production.
func TestWritePathsNeedOneConnection(t *testing.T) {
	db := fileTestDB(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	en, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		ctx := context.Background()
		steps := []func() error{
			func() error {
				return en.GrantNormalizedObjectPermissions(ctx, "u-1", "catalog", "c-1", []string{"view_detail", "modify"})
			},
			func() error { return en.AssignRole("u-2", "r-a") },
			func() error { return en.GrantRolePermission("r-a", "catalog", "*", "view_detail") },
			func() error { return en.RevokeObjectPermission("u-1", "catalog", "c-1", "modify") },
			func() error {
				return en.Transaction(ctx, func(tx *PolicyTransaction) error {
					if _, err := tx.Check("u-2", "catalog", "c-1", "view_detail"); err != nil {
						return err
					}
					return tx.GrantObjectPermission("u-3", "catalog", "c-3", "view_detail")
				})
			},
			func() error { return en.RemoveResourcePolicies("catalog", "c-3") },
			func() error { return en.RemoveRoleCompletely("r-a") },
			func() error { _, err := en.ReloadPolicy(ctx); return err },
		}
		for i, step := range steps {
			if err := step(); err != nil {
				done <- fmt.Errorf("step %d: %w", i, err)
				return
			}
		}
		done <- nil
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("a write path blocked on a second database connection")
	}
}

// TestReloadPolicyReportsDrift covers the periodic refresh: a rule written
// behind bkn-safe's back becomes live on reload, and the reload says so.
func TestReloadPolicyReportsDrift(t *testing.T) {
	db := fileTestDB(t)
	en, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if drifted, err := en.ReloadPolicy(ctx); err != nil || drifted {
		t.Fatalf("clean store: drifted=%v err=%v", drifted, err)
	}
	grant := deterministicPolicyGrant("u-out", "catalog:c-out", "view_detail", EffectAllow,
		PolicySourceLegacy, AuthoritySourceMigration)
	if err := db.Create(&gormadapter.CasbinRule{Ptype: "p", V0: grant.AccessorID, V1: grant.Object,
		V2: grant.Operation, V3: grant.Effect, V4: string(grant.PolicySource), V5: string(grant.AuthoritySource)}).Error; err != nil {
		t.Fatal(err)
	}
	row := grantModel(grant)
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if ok, _ := en.Check("u-out", "catalog", "c-out", "view_detail"); ok {
		t.Fatal("out-of-band rule was live before any reload")
	}
	if drifted, err := en.ReloadPolicy(ctx); err != nil || !drifted {
		t.Fatalf("after out-of-band write: drifted=%v err=%v", drifted, err)
	}
	if ok, _ := en.Check("u-out", "catalog", "c-out", "view_detail"); !ok {
		t.Fatal("out-of-band rule not live after reload")
	}
	if drifted, err := en.ReloadPolicy(ctx); err != nil || drifted {
		t.Fatalf("second reload: drifted=%v err=%v", drifted, err)
	}
}

var benchCatalogOps = []string{"view_detail", "create", "modify", "delete", "authorize", "task_manage", "query_data", "resource_manage"}

// BenchmarkCatalogCreatorGrant measures the write #1511 is about — a catalog
// creator's eight operations — against 27k existing rules, the reported store.
// Before #1511 it cost about three full reloads of those rules.
func BenchmarkCatalogCreatorGrant(b *testing.B) {
	db := fileTestDB(b)
	if _, err := gormadapter.NewAdapterByDB(db); err != nil { // creates casbin_rule
		b.Fatal(err)
	}
	const existing = 27000
	rules := make([]gormadapter.CasbinRule, 0, existing)
	grants := make([]model.AuthorizationGrant, 0, existing)
	for i := 0; i < existing; i++ {
		g := deterministicPolicyGrant(fmt.Sprintf("u-seed-%d", i%500), fmt.Sprintf("catalog:seed-%d", i/8),
			benchCatalogOps[i%8], EffectAllow, PolicySourceLegacy, AuthoritySourceMigration)
		rules = append(rules, gormadapter.CasbinRule{Ptype: "p", V0: g.AccessorID, V1: g.Object, V2: g.Operation,
			V3: g.Effect, V4: string(g.PolicySource), V5: string(g.AuthoritySource)})
		grants = append(grants, grantModel(g))
	}
	if err := db.CreateInBatches(rules, 1000).Error; err != nil {
		b.Fatal(err)
	}
	if err := db.CreateInBatches(grants, 1000).Error; err != nil {
		b.Fatal(err)
	}
	en, err := New(db)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := en.GrantNormalizedObjectPermissions(context.Background(), "u-bench", "catalog",
			fmt.Sprintf("bench-%d", i), benchCatalogOps); err != nil {
			b.Fatal(err)
		}
	}
}
