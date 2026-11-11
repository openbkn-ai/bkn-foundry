// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knevent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/driveradapters"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

func TestEventRoutesReachTheService(t *testing.T) {
	permissions := &permissionsStub{deny: true}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	driveradapters.RegisterKnEventRoutes(engine, &common.AppSetting{}, newTestService(permissions))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/bkn-backend/in/v1/knowledge-networks/kn1/events", nil)
	request.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_ID, "u1")
	request.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_TYPE, "user")
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden || len(permissions.checked) != 1 || permissions.checked[0] != "kn1:query_data" {
		t.Fatalf("status %d checked %v, want the service's 403", recorder.Code, permissions.checked)
	}
}

func TestNetworkDeletionRemovesEvents(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectBegin()
	for _, table := range []string{headTable, changeTable, eventTable, checkTable, typeTable} {
		mock.ExpectExec("DELETE FROM " + table + " WHERE f_kn_id = \\?").WithArgs("kn1").WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectCommit()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var cleanup interfaces.KnEventCleanupAccess = newStore(db)
	if err := cleanup.DeleteKnEvents(context.Background(), tx, "kn1"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
