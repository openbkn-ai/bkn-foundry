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

func TestSetSyncResultRejectsStaleModelVersion(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	access := &access{db: db}
	mock.ExpectExec("UPDATE t_kn_proxy_account SET").WillReturnResult(sqlmock.NewResult(0, 0))

	updated, err := access.SetSyncResult(t.Context(), "kn-1", "old-version", interfaces.KNProxySyncReady,
		"old-version", "", 1)
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

func TestEnsureResolvesConcurrentIdenticalInsert(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	access := &access{db: db}
	columns := proxyColumns()
	mock.ExpectQuery("SELECT .+ FROM t_kn_proxy_account WHERE").WithArgs("kn-1").
		WillReturnRows(sqlmock.NewRows(columns))
	mock.ExpectExec("INSERT INTO t_kn_proxy_account").WillReturnError(errors.New("duplicate key"))
	mock.ExpectQuery("SELECT .+ FROM t_kn_proxy_account WHERE").WithArgs("kn-1").
		WillReturnRows(sqlmock.NewRows(columns).AddRow(
			"kn-1", "proxy-1", "app", "active", int64(1), "pending", "", "", "", "", "", int64(0), int64(1), int64(1),
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
