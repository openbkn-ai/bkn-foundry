// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authz

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/casbin/casbin/v2"
)

// holdSharedLock stands in for another decision in flight: it holds the enforcer's read lock until
// the returned release is called.
func holdSharedLock(t *testing.T, e *Enforcer) (release func()) {
	t.Helper()
	synced, ok := e.e.(*casbin.SyncedEnforcer)
	if !ok {
		t.Fatalf("enforcer is %T, want *casbin.SyncedEnforcer", e.e)
	}
	lock := synced.GetLock()
	lock.RLock()
	var once sync.Once
	return func() { once.Do(lock.RUnlock) }
}

// finishesWhileShared fails the test when read completes only after the shared lock is released,
// i.e. when it needed the exclusive lock.
func finishesWhileShared(t *testing.T, e *Enforcer, read func() error) {
	t.Helper()
	release := holdSharedLock(t, e)
	defer release()
	done := make(chan error, 1)
	go func() { done <- read() }()
	select {
	case err := <-done:
		mustNoErr(t, err)
	case <-time.After(2 * time.Second):
		release()
		<-done
		t.Fatal("read waited for the exclusive enforcer lock while another read held the shared lock")
	}
}

// Reading an accessor's policy must not serialize decisions: before this, every decision took the
// enforcer's exclusive lock and concurrent decisions queued one behind another (#1554).
func TestPolicyReadsDoNotTakeTheExclusiveLock(t *testing.T) {
	e := newTestEnforcer(t)
	const user = "reader"
	mustNoErr(t, e.GrantObjectPermission(user, "resource", "r-1", "view_detail"))

	t.Run("resource filter", func(t *testing.T) {
		finishesWhileShared(t, e, func() error {
			_, err := e.FilterResourceOps(user, []ResourceRef{{Type: "resource", ID: "r-1"}},
				[]string{"view_detail"}, nil)
			return err
		})
	})
	t.Run("effective permissions", func(t *testing.T) {
		finishesWhileShared(t, e, func() error {
			_, _, err := e.EffectivePermissions(user, PermQuery{})
			return err
		})
	})
	t.Run("accessible resources", func(t *testing.T) {
		finishesWhileShared(t, e, func() error {
			_, err := e.AccessibleResources(user, "resource", "view_detail")
			return err
		})
	})
}

func newSuperAdmin(t *testing.T, e *Enforcer, id string, directGrants int) {
	t.Helper()
	mustNoErr(t, e.AssignRole(id, SuperAdminRoleID))
	mustNoErr(t, e.GrantRolePermission(SuperAdminRoleID, "", "*", ActAll))
	for i := 0; i < directGrants; i++ {
		mustNoErr(t, e.GrantObjectPermission(id, "resource", fmt.Sprintf("r-%d", i), "view_detail"))
	}
}

// A super-admin's decisions never read its policy rows, so the index must not load them: on a real
// deployment the super-admin held 130k migrated per-resource grants, and copying them out cost
// every one of its decisions tens of milliseconds (#1554).
func TestGrantIndexDoesNotLoadRowsForSuperAdmin(t *testing.T) {
	e := newTestEnforcer(t)
	const admin = "admin"
	newSuperAdmin(t, e, admin, 20)
	mustNoErr(t, e.DenyObjectPermission(admin, "resource", "r-1", "delete"))

	idx, err := e.grantIndex(admin)
	mustNoErr(t, err)
	if !idx.superAdmin {
		t.Fatal("super-admin not recognized")
	}
	if len(idx.exact) != 0 || len(idx.wildcard) != 0 {
		t.Fatalf("super-admin index loaded %d exact and %d wildcard rows", len(idx.exact), len(idx.wildcard))
	}
	if want := []string{admin, SuperAdminRoleID, PublicAccessorID}; !reflect.DeepEqual(idx.subjects, want) {
		t.Fatalf("subjects = %v, want %v", idx.subjects, want)
	}

	// Decisions are what they were: everything allowed, the deny included.
	got, err := e.FilterResourceOps(admin, []ResourceRef{
		{Type: "resource", ID: "r-1"},
		{Type: "resource", ID: "never-granted"},
	}, []string{"view_detail"}, []string{"view_detail", "delete"})
	mustNoErr(t, err)
	ops := opsOf(t, got)
	for _, id := range []string{"r-1", "never-granted"} {
		if want := []string{"view_detail", "delete"}; !reflect.DeepEqual(ops[id], want) {
			t.Fatalf("%s ops = %v, want %v", id, ops[id], want)
		}
	}
}

// Public policies are merged once by permissionsWithPublic. grantIndex must
// not append them again, or every list-page decision needlessly indexes each
// public grant twice.
func TestGrantIndexIncludesEachPublicPolicyOnce(t *testing.T) {
	e := newTestEnforcer(t)
	mustNoErr(t, e.GrantObjectPermission(PublicAccessorID, "resource", "public-1", "view_detail"))

	idx, err := e.grantIndex("ordinary-user")
	mustNoErr(t, err)
	rows := idx.exact["resource:public-1"]
	if len(rows) != 1 {
		t.Fatalf("public policy rows = %d, want 1: %+v", len(rows), rows)
	}
	if rows[0].act != "view_detail" || rows[0].effect != EffectAllow {
		t.Fatalf("public policy row = %+v, want view_detail allow", rows[0])
	}
}

// Explain lists the concrete grants behind a decision; it reads them itself, so the super-admin
// shortcut must leave it unchanged.
func TestExplainStillListsSuperAdminGrants(t *testing.T) {
	e := newTestEnforcer(t)
	const admin = "admin"
	newSuperAdmin(t, e, admin, 3)

	explanation, err := e.ExplainOperation(context.Background(), admin, "resource", "r-1", "view_detail")
	mustNoErr(t, err)
	if len(explanation.Steps) == 0 {
		t.Fatal("no explanation steps")
	}
	var direct bool
	for _, grant := range explanation.Steps[0].MatchedGrants {
		if grant.SubjectID == admin && grant.Object == "resource:r-1" && grant.Operation == "view_detail" {
			direct = true
		}
	}
	if !direct {
		t.Fatalf("matched grants = %+v, want the direct grant on r-1", explanation.Steps[0].MatchedGrants)
	}
}

// Decisions run concurrently with each other and with policy writes. Run with -race; an accessor
// absent from the role graph exercises casbin's temporary role creation on the read path.
func TestConcurrentDecisionsAndWrites(t *testing.T) {
	e, db := newTestEnforcerDB(t)
	// Every new connection to an in-memory sqlite database opens an empty one.
	sqlDB, err := db.DB()
	mustNoErr(t, err)
	sqlDB.SetMaxOpenConns(1)
	const admin, user = "admin", "reader"
	newSuperAdmin(t, e, admin, 5)
	mustNoErr(t, e.GrantObjectPermission(user, "resource", "r-1", "view_detail"))

	resources := []ResourceRef{{Type: "resource", ID: "r-1"}, {Type: "resource", ID: "r-2"}}
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				for _, accessor := range []string{admin, user, fmt.Sprintf("stranger-%d", i)} {
					if _, err := e.FilterResourceOps(accessor, resources, []string{"view_detail"}, nil); err != nil {
						errs <- err
						return
					}
				}
			}
		}(i)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 20; j++ {
			if err := e.GrantObjectPermission(user, "resource", fmt.Sprintf("w-%d", j), "view_detail"); err != nil {
				errs <- err
				return
			}
			if err := e.AssignRole(fmt.Sprintf("member-%d", j), "some-role"); err != nil {
				errs <- err
				return
			}
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	got, err := e.FilterResourceOps(user, []ResourceRef{{Type: "resource", ID: "w-19"}}, []string{"view_detail"}, nil)
	mustNoErr(t, err)
	if len(got) != 1 {
		t.Fatalf("grant written during concurrent reads is not visible: %v", got)
	}
}
