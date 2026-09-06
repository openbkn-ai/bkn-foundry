// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package proxygrant_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/database"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/managedproxy"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/proxygrant"
)

type fixture struct {
	db       *gorm.DB
	enforcer *authz.Enforcer
	service  *proxygrant.Service
	proxyID  string
	grantor  string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	enforcer, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	proxy, _, err := managedproxy.New(db).Create(t.Context(), managedproxy.CreateRequest{
		ManagedResourceType: managedproxy.ResourceKnowledgeNetwork,
		ManagedResourceID:   "kn-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	const grantor = "grantor-1"
	if err := db.Create(&model.User{ID: grantor, Account: grantor, Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ResourceType{ID: "resource", Name: "Resource"}).Error; err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"authorize", "view_detail", "query_data"} {
		if err := db.Create(&model.Operation{ResourceTypeID: "resource", ID: operation, Name: operation}).Error; err != nil {
			t.Fatal(err)
		}
	}
	return fixture{
		db: db, enforcer: enforcer, service: proxygrant.New(db, enforcer),
		proxyID: proxy.ProxyAccountID, grantor: grantor,
	}
}

func (f fixture) authorize(t *testing.T, resourceID string, operations ...string) {
	t.Helper()
	if err := f.enforcer.GrantObjectPermission(f.grantor, "resource", resourceID, "authorize"); err != nil {
		t.Fatal(err)
	}
	for _, operation := range operations {
		if err := f.enforcer.GrantObjectPermission(f.grantor, "resource", resourceID, operation); err != nil {
			t.Fatal(err)
		}
	}
}

func (f fixture) grantOperations(t *testing.T, accessorID, resourceID string, operations ...string) {
	f.grantTypedOperations(t, accessorID, "resource", resourceID, operations...)
}

func (f fixture) grantTypedOperations(t *testing.T, accessorID, resourceType, resourceID string, operations ...string) {
	t.Helper()
	for _, operation := range operations {
		if err := f.enforcer.GrantObjectPermission(accessorID, resourceType, resourceID, operation); err != nil {
			t.Fatal(err)
		}
	}
}

func (f fixture) request(sourceID, bindingID, resourceID string) proxygrant.GrantRequest {
	return proxygrant.GrantRequest{
		ProxyAccountID: f.proxyID,
		GrantorID:      f.grantor,
		Source: proxygrant.SourceSpec{
			ResourceType: "resource", ResourceID: resourceID, Operation: "query_data",
			SourceType: proxygrant.SourceTypeKNProxyBinding, SourceID: sourceID,
			KNID: "kn-1", BindingType: "object_type", BindingID: bindingID,
		},
	}
}

func TestGrantIsIdempotentAndLastSourceRevokesOwnedPolicy(t *testing.T) {
	f := newFixture(t)
	f.authorize(t, "r-1", "query_data")

	first, changed, err := f.service.Grant(t.Context(), f.request("source-1", "ot-1", "r-1"))
	if err != nil || !changed {
		t.Fatalf("first Grant() = (%+v, %v, %v)", first, changed, err)
	}
	replay, changed, err := f.service.Grant(t.Context(), f.request("source-1", "ot-1", "r-1"))
	if err != nil || changed || replay.ID != first.ID {
		t.Fatalf("replayed Grant() = (%+v, %v, %v), first=%+v", replay, changed, err, first)
	}
	second, changed, err := f.service.Grant(t.Context(), f.request("source-2", "ot-2", "r-1"))
	if err != nil || !changed {
		t.Fatalf("second Grant() = (%+v, %v, %v)", second, changed, err)
	}
	assertAllowed(t, f, true)

	if _, changed, err := f.service.Revoke(t.Context(), first.ID, proxygrant.RevokeRequest{GrantorID: f.grantor}); err != nil || !changed {
		t.Fatalf("revoke first = (%v, %v)", changed, err)
	}
	assertAllowed(t, f, true)
	if _, changed, err := f.service.Revoke(t.Context(), second.ID, proxygrant.RevokeRequest{GrantorID: f.grantor}); err != nil || !changed {
		t.Fatalf("revoke second = (%v, %v)", changed, err)
	}
	assertAllowed(t, f, false)

	var audits []model.ProxyGrantAuditLog
	if err := f.db.Order("created_at").Find(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if len(audits) != 5 || audits[0].GrantorID != f.grantor || audits[0].ProxyAccountID != f.proxyID ||
		audits[0].ResourceID != "r-1" || audits[0].Operation != "query_data" || audits[0].Decision != "allow" {
		t.Fatalf("audit rows = %+v", audits)
	}
}

func TestSourceIdentityCollisionIsRejected(t *testing.T) {
	f := newFixture(t)
	f.authorize(t, "r-1", "query_data")
	original := f.request("source-1", "ot-1", "r-1")
	if _, _, err := f.service.Grant(t.Context(), original); err != nil {
		t.Fatal(err)
	}
	collision := f.request("source-1", "ot-other", "r-1")
	if _, _, err := f.service.Grant(t.Context(), collision); !errors.Is(err, proxygrant.ErrInvalidRequest) {
		t.Fatalf("colliding Grant() error = %v, want invalid request", err)
	}
	if _, err := f.service.Sync(t.Context(), proxygrant.SyncRequest{
		ProxyAccountID: f.proxyID, GrantorID: f.grantor,
		Sources: []proxygrant.SourceSpec{original.Source, collision.Source},
	}); !errors.Is(err, proxygrant.ErrInvalidRequest) {
		t.Fatalf("colliding Sync() error = %v, want invalid request", err)
	}
	if _, err := f.service.Sync(t.Context(), proxygrant.SyncRequest{
		ProxyAccountID: f.proxyID, GrantorID: f.grantor,
		Sources: []proxygrant.SourceSpec{collision.Source},
	}); !errors.Is(err, proxygrant.ErrInvalidRequest) {
		t.Fatalf("existing-source collision Sync() error = %v, want invalid request", err)
	}
	var sources []model.ProxyGrantSource
	if err := f.db.Find(&sources).Error; err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].BindingID != "ot-1" {
		t.Fatalf("sources after collision = %+v", sources)
	}
}

func TestRevokingLastSourcePreservesButDoesNotTrustUntrackedPolicy(t *testing.T) {
	f := newFixture(t)
	f.authorize(t, "r-1", "query_data")
	if err := f.enforcer.GrantObjectPermission(f.proxyID, "resource", "r-1", "query_data"); err != nil {
		t.Fatal(err)
	}
	source, _, err := f.service.Grant(t.Context(), f.request("source-manual", "ot-manual", "r-1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.service.Revoke(t.Context(), source.ID, proxygrant.RevokeRequest{GrantorID: f.grantor}); err != nil {
		t.Fatal(err)
	}
	assertAllowed(t, f, false)
	var markers int64
	if err := f.db.Model(&model.ProxyGrantPolicy{}).Count(&markers).Error; err != nil || markers != 0 {
		t.Fatalf("markers = %d err=%v, want 0", markers, err)
	}
}

func TestKNBindingDerivesFromOperationWithoutAuthorize(t *testing.T) {
	f := newFixture(t)
	f.grantOperations(t, f.grantor, "r-1", "query_data")

	result, err := f.service.Check(t.Context(), f.request("source-operation", "ot-operation", "r-1"))
	if err != nil || !result.Allowed {
		t.Fatalf("operation-only Check() = (%+v, %v), want allowed", result, err)
	}
	source, changed, err := f.service.Grant(t.Context(), f.request("source-operation", "ot-operation", "r-1"))
	if err != nil || !changed || source.GrantedBy != f.grantor {
		t.Fatalf("operation-only Grant() = (%+v, %v, %v)", source, changed, err)
	}
	assertAllowed(t, f, true)
}

func TestKNBindingDerivesExecuteOperationsWithoutAuthorize(t *testing.T) {
	f := newFixture(t)
	for _, resourceType := range []string{"tool_box", "mcp"} {
		if err := f.db.Create(&model.ResourceType{ID: resourceType, Name: resourceType}).Error; err != nil {
			t.Fatal(err)
		}
		if err := f.db.Create(&model.Operation{ResourceTypeID: resourceType, ID: "execute", Name: "execute"}).Error; err != nil {
			t.Fatal(err)
		}
	}

	for _, test := range []struct {
		resourceType string
		resourceID   string
	}{
		{resourceType: "tool_box", resourceID: "box-1"},
		{resourceType: "mcp", resourceID: "mcp-1"},
	} {
		t.Run(test.resourceType, func(t *testing.T) {
			f.grantTypedOperations(t, f.grantor, test.resourceType, test.resourceID, "execute")
			request := f.request("source-"+test.resourceType, "binding-"+test.resourceType, test.resourceID)
			request.Source.ResourceType = test.resourceType
			request.Source.Operation = "execute"
			request.Source.BindingType = "action_type"
			decision, err := f.service.Check(t.Context(), request)
			if err != nil || !decision.Allowed {
				t.Fatalf("operation-only Check() = (%+v, %v), want allowed", decision, err)
			}
			if _, changed, err := f.service.Grant(t.Context(), request); err != nil || !changed {
				t.Fatalf("operation-only Grant() = changed %v, err %v", changed, err)
			}
			allowed, err := f.enforcer.Check(f.proxyID, test.resourceType, test.resourceID, "execute")
			if err != nil || !allowed {
				t.Fatalf("proxy execute = %v, err %v", allowed, err)
			}
		})
	}
}

func TestKNBindingRejectsAuthorizeWithoutOperation(t *testing.T) {
	f := newFixture(t)
	f.authorize(t, "r-1")

	result, err := f.service.Check(t.Context(), f.request("source-no-operation", "ot-no-operation", "r-1"))
	if err != nil || result.Allowed {
		t.Fatalf("authorize-only Check() = (%+v, %v), want denied", result, err)
	}
	if _, _, err := f.service.Grant(t.Context(), f.request("source-no-operation", "ot-no-operation", "r-1")); !errors.Is(err, proxygrant.ErrForbidden) {
		t.Fatalf("authorize-only Grant() error = %v, want forbidden", err)
	}
}

func TestKNBindingFollowsCurrentDelegatorPermission(t *testing.T) {
	f := newFixture(t)
	f.grantOperations(t, f.grantor, "r-1", "query_data")
	request := f.request("source-current", "ot-current", "r-1")
	if _, _, err := f.service.Grant(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	assertAllowed(t, f, true)

	if err := f.enforcer.RevokeObjectPermission(f.grantor, "resource", "r-1", "query_data"); err != nil {
		t.Fatal(err)
	}
	assertAllowed(t, f, false)
	var source model.ProxyGrantSource
	if err := f.db.First(&source, "source_id = ?", request.Source.SourceID).Error; err != nil {
		t.Fatal(err)
	}
	if source.LifecycleStatus != proxygrant.StatusActive {
		t.Fatalf("source status = %q, want active model binding", source.LifecycleStatus)
	}

	f.grantOperations(t, f.grantor, "r-1", "query_data")
	assertAllowed(t, f, true)
}

func TestKNBindingRemainsAllowedWhileAnotherDelegatorIsValid(t *testing.T) {
	f := newFixture(t)
	f.grantOperations(t, f.grantor, "r-1", "query_data")
	if _, _, err := f.service.Grant(t.Context(), f.request("source-a", "ot-a", "r-1")); err != nil {
		t.Fatal(err)
	}

	const second = "builder-2"
	if err := f.db.Create(&model.User{ID: second, Account: second, Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	f.grantOperations(t, second, "r-1", "query_data")
	secondRequest := f.request("source-b", "ot-b", "r-1")
	secondRequest.GrantorID = second
	if _, _, err := f.service.Grant(t.Context(), secondRequest); err != nil {
		t.Fatal(err)
	}

	if err := f.enforcer.RevokeObjectPermission(f.grantor, "resource", "r-1", "query_data"); err != nil {
		t.Fatal(err)
	}
	assertAllowed(t, f, true)
	if err := f.enforcer.RevokeObjectPermission(second, "resource", "r-1", "query_data"); err != nil {
		t.Fatal(err)
	}
	assertAllowed(t, f, false)
}

func TestKNBindingTracksRoleAndAccountState(t *testing.T) {
	f := newFixture(t)
	const role = "resource-reader"
	if err := f.enforcer.GrantRolePermission(role, "resource", "r-1", "query_data"); err != nil {
		t.Fatal(err)
	}
	if err := f.enforcer.AssignRole(f.grantor, role); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.service.Grant(t.Context(), f.request("source-role", "ot-role", "r-1")); err != nil {
		t.Fatal(err)
	}
	assertAllowed(t, f, true)

	if err := f.enforcer.RemoveRole(f.grantor, role); err != nil {
		t.Fatal(err)
	}
	assertAllowed(t, f, false)
	if err := f.enforcer.AssignRole(f.grantor, role); err != nil {
		t.Fatal(err)
	}
	assertAllowed(t, f, true)

	if err := f.db.Model(&model.User{}).Where("id = ?", f.grantor).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	assertAllowed(t, f, false)
}

func TestKNBindingTracksInheritedOperation(t *testing.T) {
	f := newFixture(t)
	if err := f.db.Create(&model.ResourceType{ID: "catalog", Name: "Catalog"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Create(&model.Operation{ResourceTypeID: "catalog", ID: "query_data", Name: "query_data"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.ResourceType{}).Where("id = ?", "resource").Update("parent_type_id", "catalog").Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.Operation{}).
		Where("resource_type_id = ? AND id = ?", "resource", "query_data").
		Update("parent_operation_id", "query_data").Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Create(&model.ResourceParent{
		ResourceTypeID: "resource", ResourceID: "r-1", ParentTypeID: "catalog", ParentID: "catalog-1",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.enforcer.GrantObjectPermission(f.grantor, "catalog", "catalog-1", "query_data"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.service.Grant(t.Context(), f.request("source-inherited", "ot-inherited", "r-1")); err != nil {
		t.Fatal(err)
	}
	assertAllowed(t, f, true)

	if err := f.db.Where("resource_type_id = ? AND resource_id = ?", "resource", "r-1").
		Delete(&model.ResourceParent{}).Error; err != nil {
		t.Fatal(err)
	}
	assertAllowed(t, f, false)
}

func TestKNBindingTracksOperationRegistration(t *testing.T) {
	f := newFixture(t)
	f.grantOperations(t, f.grantor, "r-1", "query_data")
	if _, _, err := f.service.Grant(t.Context(), f.request("source-operation-catalog", "ot-operation-catalog", "r-1")); err != nil {
		t.Fatal(err)
	}
	assertAllowed(t, f, true)

	if err := f.db.Where("resource_type_id = ? AND id = ?", "resource", "query_data").
		Delete(&model.Operation{}).Error; err != nil {
		t.Fatal(err)
	}
	assertAllowed(t, f, false)
}

func TestSyncTransfersInvalidHistoricalDelegator(t *testing.T) {
	f := newFixture(t)
	f.grantOperations(t, f.grantor, "r-1", "query_data")
	request := f.request("source-transfer", "ot-transfer", "r-1")
	if _, _, err := f.service.Grant(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if err := f.enforcer.RevokeObjectPermission(f.grantor, "resource", "r-1", "query_data"); err != nil {
		t.Fatal(err)
	}

	const replacement = "builder-2"
	if err := f.db.Create(&model.User{ID: replacement, Account: replacement, Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	f.grantOperations(t, replacement, "r-1", "query_data")
	preflight := request
	preflight.GrantorID = replacement
	decision, err := f.service.Check(t.Context(), preflight)
	if err != nil || !decision.Allowed {
		t.Fatalf("replacement Check() = (%+v, %v), want allowed", decision, err)
	}
	result, err := f.service.Sync(t.Context(), proxygrant.SyncRequest{
		ProxyAccountID: f.proxyID, GrantorID: replacement, Sources: []proxygrant.SourceSpec{request.Source},
	})
	if err != nil || result.Transferred != 1 || result.Unchanged != 0 {
		t.Fatalf("replacement Sync() = (%+v, %v)", result, err)
	}
	var source model.ProxyGrantSource
	if err := f.db.First(&source, "source_id = ?", request.Source.SourceID).Error; err != nil {
		t.Fatal(err)
	}
	if source.GrantedBy != replacement {
		t.Fatalf("source delegator = %q, want %q", source.GrantedBy, replacement)
	}
	assertAllowed(t, f, true)
}

func TestSyncPreservesValidHistoricalDelegator(t *testing.T) {
	f := newFixture(t)
	f.grantOperations(t, f.grantor, "r-1", "query_data")
	request := f.request("source-preserve", "ot-preserve", "r-1")
	if _, _, err := f.service.Grant(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	const editor = "builder-without-data"
	if err := f.db.Create(&model.User{ID: editor, Account: editor, Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	preflight := request
	preflight.GrantorID = editor
	decision, err := f.service.Check(t.Context(), preflight)
	if err != nil || !decision.Allowed {
		t.Fatalf("historical Check() = (%+v, %v), want allowed", decision, err)
	}
	result, err := f.service.Sync(t.Context(), proxygrant.SyncRequest{
		ProxyAccountID: f.proxyID, GrantorID: editor, Sources: []proxygrant.SourceSpec{request.Source},
	})
	if err != nil || result.Unchanged != 1 || result.Transferred != 0 {
		t.Fatalf("historical Sync() = (%+v, %v)", result, err)
	}
	var source model.ProxyGrantSource
	if err := f.db.First(&source, "source_id = ?", request.Source.SourceID).Error; err != nil {
		t.Fatal(err)
	}
	if source.GrantedBy != f.grantor {
		t.Fatalf("source delegator = %q, want original %q", source.GrantedBy, f.grantor)
	}
}

func TestGrantTransfersInvalidHistoricalDelegator(t *testing.T) {
	f := newFixture(t)
	f.grantOperations(t, f.grantor, "r-1", "query_data")
	request := f.request("source-grant-transfer", "ot-grant-transfer", "r-1")
	if _, _, err := f.service.Grant(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if err := f.enforcer.RevokeObjectPermission(f.grantor, "resource", "r-1", "query_data"); err != nil {
		t.Fatal(err)
	}

	const replacement = "builder-grant-replacement"
	if err := f.db.Create(&model.User{ID: replacement, Account: replacement, Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	f.grantOperations(t, replacement, "r-1", "query_data")
	request.GrantorID = replacement
	source, changed, err := f.service.Grant(t.Context(), request)
	if err != nil || !changed || source.GrantedBy != replacement {
		t.Fatalf("replacement Grant() = (%+v, %v, %v)", source, changed, err)
	}
	assertAllowed(t, f, true)
}

func TestGrantPreservesValidHistoricalDelegator(t *testing.T) {
	f := newFixture(t)
	f.grantOperations(t, f.grantor, "r-1", "query_data")
	request := f.request("source-grant-preserve", "ot-grant-preserve", "r-1")
	if _, _, err := f.service.Grant(t.Context(), request); err != nil {
		t.Fatal(err)
	}

	const editor = "builder-grant-without-data"
	if err := f.db.Create(&model.User{ID: editor, Account: editor, Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	request.GrantorID = editor
	source, changed, err := f.service.Grant(t.Context(), request)
	if err != nil || changed || source.GrantedBy != f.grantor {
		t.Fatalf("historical Grant() = (%+v, %v, %v)", source, changed, err)
	}
}

func TestReconcileReportsInvalidSourceAndSyncRestoresMaterialization(t *testing.T) {
	f := newFixture(t)
	f.grantOperations(t, f.grantor, "r-1", "query_data")
	request := f.request("source-reconcile-invalid", "ot-reconcile-invalid", "r-1")
	if _, _, err := f.service.Grant(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if err := f.enforcer.RevokeObjectPermission(f.grantor, "resource", "r-1", "query_data"); err != nil {
		t.Fatal(err)
	}
	result, err := f.service.Reconcile(t.Context(), proxygrant.ReconcileRequest{
		ProxyAccountID: f.proxyID, RequestedBy: "system:reconcile",
	})
	if err != nil || result.InvalidSources != 1 || result.PoliciesRemoved != 1 {
		t.Fatalf("invalid Reconcile() = (%+v, %v)", result, err)
	}
	assertAllowed(t, f, false)

	f.grantOperations(t, f.grantor, "r-1", "query_data")
	syncResult, err := f.service.Sync(t.Context(), proxygrant.SyncRequest{
		ProxyAccountID: f.proxyID, GrantorID: f.grantor, Sources: []proxygrant.SourceSpec{request.Source},
	})
	if err != nil || syncResult.Unchanged != 1 {
		t.Fatalf("restoring Sync() = (%+v, %v)", syncResult, err)
	}
	assertAllowed(t, f, true)
}

func TestRevokingBindingPreservesActiveManualSource(t *testing.T) {
	f := newFixture(t)
	f.authorize(t, "r-1", "query_data")
	binding, _, err := f.service.Grant(t.Context(), f.request("source-binding", "ot-binding", "r-1"))
	if err != nil {
		t.Fatal(err)
	}
	manualRequest := f.request("source-manual", "manual-ticket", "r-1")
	manualRequest.Source.SourceType = proxygrant.SourceTypeManual
	manualRequest.Source.BindingType = "manual"
	manual, _, err := f.service.Grant(t.Context(), manualRequest)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.service.Revoke(t.Context(), binding.ID, proxygrant.RevokeRequest{GrantorID: f.grantor}); err != nil {
		t.Fatal(err)
	}
	assertAllowed(t, f, true)
	if _, _, err := f.service.Revoke(t.Context(), manual.ID, proxygrant.RevokeRequest{GrantorID: f.grantor}); err != nil {
		t.Fatal(err)
	}
	assertAllowed(t, f, false)
}

func TestFullBindingSyncPreservesManualSource(t *testing.T) {
	f := newFixture(t)
	f.authorize(t, "r-1", "query_data")
	binding := f.request("source-binding", "ot-binding", "r-1")
	if _, _, err := f.service.Grant(t.Context(), binding); err != nil {
		t.Fatal(err)
	}
	manualRequest := f.request("source-manual", "manual-ticket", "r-1")
	manualRequest.Source.SourceType = proxygrant.SourceTypeManual
	manualRequest.Source.BindingType = "manual"
	manual, _, err := f.service.Grant(t.Context(), manualRequest)
	if err != nil {
		t.Fatal(err)
	}

	result, err := f.service.Sync(t.Context(), proxygrant.SyncRequest{
		ProxyAccountID: f.proxyID, GrantorID: f.grantor,
	})
	if err != nil || result.Revoked != 1 || len(result.Sources) != 0 {
		t.Fatalf("empty binding Sync() = (%+v, %v)", result, err)
	}
	assertAllowed(t, f, true)
	var source model.ProxyGrantSource
	if err := f.db.First(&source, "id = ?", manual.ID).Error; err != nil {
		t.Fatal(err)
	}
	if source.LifecycleStatus != proxygrant.StatusActive {
		t.Fatalf("manual source status = %q, want active", source.LifecycleStatus)
	}
}

func TestDisabledProxyAllowsCleanupButRejectsNewSource(t *testing.T) {
	f := newFixture(t)
	f.authorize(t, "r-1", "query_data")
	if _, _, err := f.service.Grant(t.Context(), f.request("source-1", "ot-1", "r-1")); err != nil {
		t.Fatal(err)
	}
	if _, err := managedproxy.New(f.db).Disable(t.Context(), f.proxyID); err != nil {
		t.Fatal(err)
	}

	cleanup, err := f.service.Sync(t.Context(), proxygrant.SyncRequest{
		ProxyAccountID: f.proxyID, GrantorID: f.grantor,
	})
	if err != nil || cleanup.Revoked != 1 {
		t.Fatalf("disabled proxy cleanup Sync() = (%+v, %v)", cleanup, err)
	}
	assertAllowed(t, f, false)

	_, err = f.service.Sync(t.Context(), proxygrant.SyncRequest{
		ProxyAccountID: f.proxyID, GrantorID: f.grantor,
		Sources: []proxygrant.SourceSpec{f.request("source-2", "ot-2", "r-1").Source},
	})
	if !errors.Is(err, proxygrant.ErrProxyInactive) {
		t.Fatalf("disabled proxy addition error = %v, want proxy inactive", err)
	}
}

func TestSyncPreflightRejectsWholeSetOnUnauthorizedTarget(t *testing.T) {
	f := newFixture(t)
	f.authorize(t, "r-1", "query_data")
	good := f.request("source-good", "ot-good", "r-1").Source
	bad := f.request("source-bad", "ot-bad", "r-2").Source

	_, err := f.service.Sync(t.Context(), proxygrant.SyncRequest{
		ProxyAccountID: f.proxyID, GrantorID: f.grantor, Sources: []proxygrant.SourceSpec{good, bad},
	})
	if !errors.Is(err, proxygrant.ErrForbidden) {
		t.Fatalf("Sync() error = %v, want forbidden", err)
	}
	var sources, markers int64
	if err := f.db.Model(&model.ProxyGrantSource{}).Count(&sources).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.ProxyGrantPolicy{}).Count(&markers).Error; err != nil {
		t.Fatal(err)
	}
	if sources != 0 || markers != 0 {
		t.Fatalf("failed sync left sources=%d markers=%d", sources, markers)
	}
	assertAllowed(t, f, false)
}

func TestAuditFailureRollsBackSourceAndPolicy(t *testing.T) {
	f := newFixture(t)
	f.authorize(t, "r-1", "query_data")
	if err := f.db.Migrator().DropTable(&model.ProxyGrantAuditLog{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.service.Grant(t.Context(), f.request("source-rollback", "ot-rollback", "r-1")); err == nil {
		t.Fatal("Grant() succeeded with missing audit table")
	}
	var sources int64
	if err := f.db.Model(&model.ProxyGrantSource{}).Count(&sources).Error; err != nil {
		t.Fatal(err)
	}
	if sources != 0 {
		t.Fatalf("failed transaction left %d sources", sources)
	}
	assertAllowed(t, f, false)
}

func TestFullSyncAndReconcileAreIdempotent(t *testing.T) {
	f := newFixture(t)
	f.authorize(t, "r-1", "query_data")
	one := f.request("source-1", "ot-1", "r-1").Source
	two := f.request("source-2", "ot-2", "r-1").Source

	first, err := f.service.Sync(t.Context(), proxygrant.SyncRequest{
		ProxyAccountID: f.proxyID, GrantorID: f.grantor, Sources: []proxygrant.SourceSpec{one, two},
	})
	if err != nil || first.Added != 2 || len(first.Sources) != 2 {
		t.Fatalf("first Sync() = (%+v, %v)", first, err)
	}
	replay, err := f.service.Sync(t.Context(), proxygrant.SyncRequest{
		ProxyAccountID: f.proxyID, GrantorID: f.grantor, Sources: []proxygrant.SourceSpec{one, two},
	})
	if err != nil || replay.Added != 0 || replay.Revoked != 0 || replay.Unchanged != 2 {
		t.Fatalf("replayed Sync() = (%+v, %v)", replay, err)
	}

	if err := f.enforcer.RevokeObjectPermission(f.proxyID, "resource", "r-1", "query_data"); err != nil {
		t.Fatal(err)
	}
	repaired, err := f.service.Reconcile(t.Context(), proxygrant.ReconcileRequest{ProxyAccountID: f.proxyID, RequestedBy: "system:reconcile"})
	if err != nil || repaired.PoliciesRestored != 1 {
		t.Fatalf("restore Reconcile() = (%+v, %v)", repaired, err)
	}
	assertAllowed(t, f, true)

	empty, err := f.service.Sync(t.Context(), proxygrant.SyncRequest{ProxyAccountID: f.proxyID, GrantorID: f.grantor})
	if err != nil || empty.Revoked != 2 || len(empty.Sources) != 0 {
		t.Fatalf("empty Sync() = (%+v, %v)", empty, err)
	}
	assertAllowed(t, f, false)
	replayEmpty, err := f.service.Sync(t.Context(), proxygrant.SyncRequest{ProxyAccountID: f.proxyID, GrantorID: f.grantor})
	if err != nil || replayEmpty.Added != 0 || replayEmpty.Revoked != 0 {
		t.Fatalf("replayed empty Sync() = (%+v, %v)", replayEmpty, err)
	}
}

func TestConcurrentGrantReplayCreatesOneSourceAndPolicy(t *testing.T) {
	f := newFixture(t)
	f.authorize(t, "r-1", "query_data")
	sqlDB, err := f.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)

	const callers = 12
	start := make(chan struct{})
	errs := make(chan error, callers)
	ids := make(chan string, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			source, _, err := f.service.Grant(t.Context(), f.request("source-concurrent", "ot-concurrent", "r-1"))
			errs <- err
			if source != nil {
				ids <- source.ID
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	close(ids)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Grant() error = %v", err)
		}
	}
	unique := map[string]bool{}
	for id := range ids {
		unique[id] = true
	}
	if len(unique) != 1 {
		t.Fatalf("source ids = %v, want one", unique)
	}
	var sources, policies int64
	if err := f.db.Model(&model.ProxyGrantSource{}).Count(&sources).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&gormadapterRule{}).Where("ptype = ? AND v0 = ? AND v1 = ? AND v2 = ?",
		"p", f.proxyID, "resource:r-1", "query_data").Count(&policies).Error; err != nil {
		t.Fatal(err)
	}
	if sources != 1 || policies != 1 {
		t.Fatalf("concurrent replay left sources=%d policies=%d", sources, policies)
	}
}

func TestCheckDenialIsAuditedWithoutMutation(t *testing.T) {
	f := newFixture(t)
	result, err := f.service.Check(t.Context(), f.request("source-check", "ot-check", "r-1"))
	if err != nil || result.Allowed || result.Reason == "" {
		t.Fatalf("Check() = (%+v, %v)", result, err)
	}
	var audit model.ProxyGrantAuditLog
	if err := f.db.Last(&audit).Error; err != nil {
		t.Fatal(err)
	}
	if audit.Decision != "deny" || audit.GrantorID != f.grantor || audit.ProxyAccountID != f.proxyID {
		t.Fatalf("audit = %+v", audit)
	}
}

func TestRevokeAndEmptySyncDenialsAreAudited(t *testing.T) {
	f := newFixture(t)
	f.authorize(t, "r-1", "query_data")
	source, _, err := f.service.Grant(t.Context(), f.request("source-audit", "ot-audit", "r-1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.service.Revoke(t.Context(), source.ID, proxygrant.RevokeRequest{GrantorID: "missing-grantor"}); !errors.Is(err, proxygrant.ErrForbidden) {
		t.Fatalf("denied Revoke() error = %v, want forbidden", err)
	}
	if _, err := f.service.Sync(t.Context(), proxygrant.SyncRequest{
		ProxyAccountID: f.proxyID, GrantorID: "missing-grantor",
	}); !errors.Is(err, proxygrant.ErrForbidden) {
		t.Fatalf("denied empty Sync() error = %v, want forbidden", err)
	}
	var audits []model.ProxyGrantAuditLog
	if err := f.db.Where("decision = ?", "deny").Order("action").Find(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if len(audits) != 2 || audits[0].Action != "revoke" || audits[1].Action != "sync" ||
		audits[0].SourceID != "source-audit" {
		t.Fatalf("denial audits = %+v", audits)
	}
}

func assertAllowed(t *testing.T, f fixture, want bool) {
	t.Helper()
	allowed, err := f.enforcer.Check(f.proxyID, "resource", "r-1", "query_data")
	if err != nil || allowed != want {
		t.Fatalf("proxy allowed = %v err=%v, want %v", allowed, err, want)
	}
	operations, err := f.enforcer.AllowedOps(f.proxyID, "resource", "r-1", []string{"query_data"})
	listed := len(operations) == 1 && operations[0] == "query_data"
	if err != nil || listed != want {
		t.Fatalf("proxy operations = %v err=%v, want query_data listed=%v", operations, err, want)
	}
	filtered, err := f.enforcer.FilterResourceOps(f.proxyID,
		[]authz.ResourceRef{{Type: "resource", ID: "r-1"}}, []string{"query_data"}, []string{"query_data"})
	visible := len(filtered) == 1 && len(filtered[0].Operations) == 1 && filtered[0].Operations[0] == "query_data"
	if err != nil || visible != want {
		t.Fatalf("proxy filtered resources = %v err=%v, want visible=%v", filtered, err, want)
	}
}

// gormadapterRule mirrors the public adapter table without importing adapter
// internals into test assertions.
type gormadapterRule struct {
	ID    uint `gorm:"primaryKey"`
	Ptype string
	V0    string
	V1    string
	V2    string
}

func (gormadapterRule) TableName() string { return "casbin_rule" }
