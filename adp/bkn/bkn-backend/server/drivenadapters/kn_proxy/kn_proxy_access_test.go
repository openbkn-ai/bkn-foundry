// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kn_proxy

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"bkn-backend/interfaces"
)

func TestMarkSyncFailedRejectsStaleGeneration(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	access := &access{db: db}
	mock.ExpectExec("UPDATE t_kn_proxy_account SET").WillReturnResult(sqlmock.NewResult(0, 0))

	updated, err := access.MarkSyncFailed(t.Context(), "kn-1", 3, "worker-1", "failure", 1)
	if err != nil {
		t.Fatal(err)
	}
	if updated {
		t.Fatal("stale synchronization result updated the current mapping")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRenewLockRejectsExpiredOrReassignedLease(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	access := &access{db: db}
	mock.ExpectExec("UPDATE t_kn_proxy_account SET").WillReturnResult(sqlmock.NewResult(0, 0))

	renewed, err := access.RenewLock(t.Context(), "kn-1", "worker-1", 100, 200)
	if err != nil {
		t.Fatal(err)
	}
	if renewed {
		t.Fatal("expired or reassigned lease was renewed")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestReplacePublishedSnapshotAndMarkReadyIsAtomic(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	access := &access{db: db}
	source := interfaces.ProxyGrantSourceSpec{
		KNID: "kn-1", BindingType: "object_type", BindingID: "ot-1",
		ResourceType: "resource", ResourceID: "resource-1", Operation: "query_data",
		SourceType: "kn_proxy_binding", SourceID: "source-1",
	}
	second := source
	second.BindingID = "ot-2"
	second.SourceID = "source-2"
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM t_kn_proxy_published_grant_source").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO t_kn_proxy_published_grant_source").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE t_kn_proxy_account SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := access.ReplacePublishedSnapshotAndMarkReady(t.Context(), "kn-1", 7, "worker-1", "sha256:ready", []interfaces.ProxyGrantSourceSpec{source, second}, 1); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDeletePublishedSnapshot(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	access := &access{db: db}
	mock.ExpectExec("DELETE FROM t_kn_proxy_published_grant_source").WithArgs("kn-1").
		WillReturnResult(sqlmock.NewResult(0, 2))

	if err := access.DeletePublishedSnapshot(t.Context(), "kn-1"); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestResolvePublishedBindingsReturnsOnlySnapshotMatches(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	access := &access{db: db}
	matched := interfaces.KNProxyBinding{
		ChildType: "object_type", ChildID: "ot-1", TargetType: "resource", TargetID: "resource-1", Operation: "query_data",
	}
	missing := matched
	missing.TargetID = "resource-missing"
	mock.ExpectQuery("SELECT f_binding_type, f_binding_id, f_resource_type, f_resource_id, f_operation FROM t_kn_proxy_published_grant_source").
		WillReturnRows(sqlmock.NewRows([]string{"f_binding_type", "f_binding_id", "f_resource_type", "f_resource_id", "f_operation"}).
			AddRow("object_type", "ot-1", "resource", "resource-1", "query_data"))

	resolved, err := access.ResolvePublishedBindings(t.Context(), "kn-1", []interfaces.KNProxyBinding{matched, missing})
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 1 || resolved[0] != matched {
		t.Fatalf("resolved = %#v, want only %#v", resolved, matched)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestResolvePublishedBindingReturnsSnapshotTargetType(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	access := &access{db: db}
	mock.ExpectQuery("SELECT f_binding_type, f_binding_id, f_resource_type, f_resource_id, f_operation FROM t_kn_proxy_published_grant_source").
		WillReturnRows(sqlmock.NewRows([]string{"f_binding_type", "f_binding_id", "f_resource_type", "f_resource_id", "f_operation"}).
			AddRow("action_type", "at-1", "function", "box-1", "execute"))

	resolved, err := access.ResolvePublishedBinding(t.Context(), "kn-1", interfaces.KNProxyBinding{
		ChildType: "action_type", ChildID: "at-1", TargetID: "box-1", Operation: "execute",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved == nil || resolved.TargetType != "function" {
		t.Fatalf("resolved binding = %#v, want the published function target", resolved)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureResolvesConcurrentIdenticalInsert(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	access := &access{db: db}
	columns := proxyColumns()
	mock.ExpectQuery("SELECT .+ FROM t_kn_proxy_account WHERE").WithArgs("kn-1").
		WillReturnRows(sqlmock.NewRows(columns))
	mock.ExpectExec("INSERT INTO t_kn_proxy_account").WillReturnError(errors.New("duplicate key"))
	mock.ExpectQuery("SELECT .+ FROM t_kn_proxy_account WHERE").WithArgs("kn-1").
		WillReturnRows(sqlmock.NewRows(columns).AddRow(
			"kn-1", "proxy-1", "app", "active", int64(1), "pending", "", "", "", int64(0), "", "", "", int64(0), int64(0), int64(0), int64(1), int64(1),
		))

	mapping, created, err := access.Ensure(t.Context(), &interfaces.KNProxyAccount{
		KNID: "kn-1", ProxyAccountID: "proxy-1", ProxyAccountType: "app", LifecycleStatus: "active",
		Version: 1, SyncStatus: "pending", CreatedAt: 1, UpdatedAt: 1,
	})
	if err != nil || created || mapping == nil || mapping.ProxyAccountID != "proxy-1" {
		t.Fatalf("Ensure() = mapping %#v, created %v, err %v", mapping, created, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
