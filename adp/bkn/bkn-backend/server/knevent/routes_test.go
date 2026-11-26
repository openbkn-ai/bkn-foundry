// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knevent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/licverify"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/driveradapters"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
)

func eventEngine(t *testing.T, edition licverify.Edition, permissions *permissionsStub) *gin.Engine {
	t.Helper()
	entitlement.SetGateForTest(entitlement.FixedGate(edition))
	t.Cleanup(entitlement.ResetForTest)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	driveradapters.RegisterKnEventRoutes(engine, &common.AppSetting{}, newTestService(permissions), gate)
	return engine
}

func serveEvents(engine *gin.Engine, path string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_ID, "u1")
	request.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_TYPE, "user")
	engine.ServeHTTP(recorder, request)
	return recorder
}

func TestEventRoutesReachTheService(t *testing.T) {
	permissions := &permissionsStub{deny: true}
	engine := eventEngine(t, licverify.EditionProfessional, permissions)
	for _, path := range []string{"/events", "/event-stats"} {
		permissions.checked = nil
		recorder := serveEvents(engine, "/api/bkn-backend/in/v1/knowledge-networks/kn1"+path)
		if recorder.Code != http.StatusForbidden || len(permissions.checked) != 1 || permissions.checked[0] != "kn1:query_data" {
			t.Fatalf("%s: status %d checked %v, want the service's 403", path, recorder.Code, permissions.checked)
		}
	}
}

func TestEventRoutesAreNotThereWithoutTheLicense(t *testing.T) {
	permissions := &permissionsStub{}
	engine := eventEngine(t, licverify.EditionCommunity, permissions)
	for _, path := range []string{
		"/api/bkn-backend/v1/knowledge-networks/kn1/events",
		"/api/bkn-backend/in/v1/knowledge-networks/kn1/events",
		"/api/bkn-backend/in/v1/knowledge-networks/kn1/event-stats",
	} {
		recorder := serveEvents(engine, path)
		if recorder.Code != http.StatusNotFound || strings.TrimSpace(recorder.Body.String()) != "404 page not found" ||
			!strings.HasPrefix(recorder.Header().Get("Content-Type"), "text/plain") {
			t.Fatalf("%s: %d %q %q, want the server's own 404", path, recorder.Code, recorder.Body.String(),
				recorder.Header().Get("Content-Type"))
		}
	}
	if len(permissions.checked) != 0 {
		t.Fatalf("checked %v without the license", permissions.checked)
	}
}

func TestNetworkDeletionRemovesEvents(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectBegin()
	for _, table := range []string{headTable, changeTable, eventTable, checkTable, handlingTable} {
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
