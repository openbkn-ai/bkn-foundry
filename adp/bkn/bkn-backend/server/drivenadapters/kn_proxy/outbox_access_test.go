// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kn_proxy

import (
	"encoding/json"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"bkn-backend/interfaces"
)

func TestStageDeltaPersistsPlanAndEventInCallerTransaction(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	access := &outboxAccess{db: db}
	source := interfaces.ProxyGrantSourceSpec{
		KNID: "kn-1", BindingType: "relation_type", BindingID: "rt-1",
		ResourceType: "resource", ResourceID: "resource-1", Operation: "query_data",
		SourceType: interfaces.ProxyGrantSourceTypeKNBinding, SourceID: "source-1",
	}
	event := &interfaces.KNProxyOutboxEvent{
		ID: "event-1", KNID: "kn-1", ProxyAccountID: "proxy-1", GrantorID: "editor-1",
		BaseVersion: "v1", TargetVersion: "v2", Upserts: []interfaces.ProxyGrantSourceSpec{source},
	}
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE t_kn_proxy_account SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT f_sync_generation FROM t_kn_proxy_account").
		WillReturnRows(sqlmock.NewRows([]string{"f_sync_generation"}).AddRow(int64(8)))
	mock.ExpectExec("DELETE FROM t_kn_proxy_planned_grant_source").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO t_kn_proxy_planned_grant_source").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO t_kn_proxy_sync_outbox").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := access.StageDelta(t.Context(), tx, event, "request-1",
		[]interfaces.KNProxyBindingRef{{BindingType: "relation_type", BindingID: "rt-1"}},
		[]interfaces.ProxyGrantSourceSpec{source}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if generation != 8 || event.Generation != 8 || event.Status != interfaces.KNProxyOutboxPending ||
		len(event.Bindings) != 1 || len(event.DesiredSources) != 1 {
		t.Fatalf("staged event = %#v, generation %d", event, generation)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCleanupDoneDeletesOnlyPublishedUnleasedEvents(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	access := &outboxAccess{db: db}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT GET_LOCK(CONCAT(DATABASE(), ':', ?), 0)")).
		WithArgs(cleanupLockName).WillReturnRows(sqlmock.NewRows([]string{"acquired"}).AddRow(1))
	mock.ExpectExec("DELETE FROM t_kn_proxy_sync_outbox").WithArgs(int64(100), 500).
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec(regexp.QuoteMeta("SELECT RELEASE_LOCK(CONCAT(DATABASE(), ':', ?))")).WithArgs(cleanupLockName).
		WillReturnResult(sqlmock.NewResult(0, 1))

	deleted, err := access.CleanupDone(t.Context(), 100, 500)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 3 {
		t.Fatalf("CleanupDone() = %d, want 3", deleted)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestClaimNextClaimsOnlyDatabaseSelectedQueueHead(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	access := &outboxAccess{db: db}
	payload, err := json.Marshal(&interfaces.KNProxyOutboxEvent{
		ID: "event-1", KNID: "kn-1", ProxyAccountID: "proxy-1", Generation: 3,
		GrantorID: "editor-1", BaseVersion: "v2", TargetVersion: "v3",
	})
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT e.f_id, e.f_kn_id, e.f_proxy_account_id, e.f_generation").
		WithArgs(int64(100), int64(100)).
		WillReturnRows(sqlmock.NewRows([]string{
			"f_id", "f_kn_id", "f_proxy_account_id", "f_generation", "f_base_version", "f_target_version",
			"f_payload", "f_attempt_count", "f_created_at",
		}).AddRow("event-1", "kn-1", "proxy-1", int64(3), "v2", "v3", payload, 1, int64(10)))
	mock.ExpectExec("UPDATE t_kn_proxy_sync_outbox SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	event, err := access.ClaimNext(t.Context(), "pod-1", 100, 160)
	if err != nil {
		t.Fatal(err)
	}
	if event == nil || event.ID != "event-1" || event.AttemptCount != 2 ||
		event.Status != interfaces.KNProxyOutboxProcessing || event.LeaseUntil != 160 {
		t.Fatalf("claimed event = %#v", event)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateOutboxPayloadRejectsDivergentDesiredSnapshot(t *testing.T) {
	source := interfaces.ProxyGrantSourceSpec{
		KNID: "kn-1", BindingType: "object_type", BindingID: "ot-1",
		ResourceType: "resource", ResourceID: "resource-1", Operation: "query_data",
		SourceType: interfaces.ProxyGrantSourceTypeKNBinding, SourceID: "source-1",
	}
	event := &interfaces.KNProxyOutboxEvent{
		KNID: "kn-1", GrantorID: "editor-1", TargetVersion: "v1",
		Bindings:       []interfaces.KNProxyBindingRef{{BindingType: "object_type", BindingID: "ot-1"}},
		DesiredSources: []interfaces.ProxyGrantSourceSpec{source},
	}
	if err := validateOutboxPayload(event); err == nil {
		t.Fatal("validateOutboxPayload() error = nil, want desired/upsert mismatch")
	}
	event.Upserts = []interfaces.ProxyGrantSourceSpec{source}
	if err := validateOutboxPayload(event); err != nil {
		t.Fatalf("validateOutboxPayload() valid event error = %v", err)
	}
}

func TestCompleteKeepsLatestPendingVersionWhenNewerGenerationExists(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	access := &outboxAccess{db: db}
	event := &interfaces.KNProxyOutboxEvent{
		ID: "event-1", KNID: "kn-1", ProxyAccountID: "proxy-1", Generation: 1,
		BaseVersion: "v0", TargetVersion: "v1",
		Bindings: []interfaces.KNProxyBindingRef{{BindingType: "object_type", BindingID: "ot-1"}},
	}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT f_proxy_account_id, f_lifecycle_status, f_published_generation").
		WithArgs("kn-1").WillReturnRows(sqlmock.NewRows([]string{
		"f_proxy_account_id", "f_lifecycle_status", "f_published_generation", "f_sync_generation",
		"f_published_model_version", "f_pending_model_version",
	}).AddRow("proxy-1", interfaces.KNProxyLifecycleActive, int64(0), int64(2), "v0", "v2"))
	mock.ExpectExec("DELETE FROM t_kn_proxy_published_grant_source").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE t_kn_proxy_sync_outbox SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE t_kn_proxy_account SET").
		WithArgs("", int64(100), "v2", int64(1), "v1", interfaces.KNProxySyncPending, "v1", int64(100), "kn-1", int64(0)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := access.Complete(t.Context(), event, "pod-1", 100); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCompleteRejectsAStaleBaseVersionBeforeChangingSnapshot(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	access := &outboxAccess{db: db}
	event := &interfaces.KNProxyOutboxEvent{
		ID: "event-1", KNID: "kn-1", ProxyAccountID: "proxy-1", Generation: 2,
		BaseVersion: "stale", TargetVersion: "v2",
	}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT f_proxy_account_id, f_lifecycle_status, f_published_generation").
		WithArgs("kn-1").WillReturnRows(sqlmock.NewRows([]string{
		"f_proxy_account_id", "f_lifecycle_status", "f_published_generation", "f_sync_generation",
		"f_published_model_version", "f_pending_model_version",
	}).AddRow("proxy-1", interfaces.KNProxyLifecycleActive, int64(1), int64(2), "v1", "v2"))
	mock.ExpectRollback()

	if err := access.Complete(t.Context(), event, "pod-1", 100); err == nil {
		t.Fatal("Complete() error = nil, want stale base rejection")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
