// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authz

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func TestResourceDeletionRollsBackPoliciesWhenParentCleanupFails(t *testing.T) {
	e, db := newTestEnforcerDB(t)
	mustNoErr(t, e.GrantObjectPermission("reader", "pipeline", "p1", "read"))
	mustNoErr(t, e.GrantObjectPermission("reader", "pipeline", "p2", "read"))
	mustNoErr(t, db.Create(&model.ResourceParent{
		ResourceTypeID: "pipeline", ResourceID: "p1", ParentTypeID: "knowledge_network", ParentID: "kn1",
	}).Error)
	injected := errors.New("parent cleanup failed")
	mustNoErr(t, db.Callback().Delete().Before("gorm:delete").Register("test:parent_failure", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "ResourceParent" {
			tx.AddError(injected)
		}
	}))
	if err := e.RemoveResourcePoliciesContext(t.Context(), "pipeline", "p1"); !errors.Is(err, injected) {
		t.Fatalf("delete error = %v, want parent failure", err)
	}
	if allowed, err := e.Check("reader", "pipeline", "p1", "read"); err != nil || !allowed {
		t.Fatalf("failed deletion changed live permission: allowed=%v err=%v", allowed, err)
	}
	var count int64
	mustNoErr(t, db.Model(&model.AuthorizationGrant{}).Where("object = ?", "pipeline:p1").Count(&count).Error)
	if count != 1 {
		t.Fatalf("durable grants = %d, want 1 after rollback", count)
	}
	mustNoErr(t, db.Callback().Delete().Remove("test:parent_failure"))
	for range 2 {
		mustNoErr(t, e.RemoveResourcePoliciesContext(t.Context(), "pipeline", "p1"))
	}
	mustNoErr(t, db.Model(&model.ResourceParent{}).Where("resource_id = ?", "p1").Count(&count).Error)
	if count != 0 {
		t.Fatalf("parent rows after retry = %d", count)
	}
	if allowed, err := e.Check("reader", "pipeline", "p1", "read"); err != nil || allowed {
		t.Fatalf("deleted permission: allowed=%v err=%v", allowed, err)
	}
	if allowed, err := e.Check("reader", "pipeline", "p2", "read"); err != nil || !allowed {
		t.Fatalf("sibling permission changed: allowed=%v err=%v", allowed, err)
	}
}

func TestResourceDeletionHonorsCanceledContext(t *testing.T) {
	e := newTestEnforcer(t)
	mustNoErr(t, e.GrantObjectPermission("reader", "pipeline", "p1", "read"))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := e.RemoveResourcePoliciesContext(ctx, "pipeline", "p1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("delete error = %v, want canceled", err)
	}
	if allowed, err := e.Check("reader", "pipeline", "p1", "read"); err != nil || !allowed {
		t.Fatalf("canceled deletion changed permission: allowed=%v err=%v", allowed, err)
	}
}
