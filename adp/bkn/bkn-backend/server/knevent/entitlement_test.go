// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knevent

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/driveradapters"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/licverify"
)

func eventEngine(s *service) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	driveradapters.RegisterKnEventRoutes(engine, &common.AppSetting{}, s, requireEntitlement())
	return engine
}

type wireResponse struct {
	status int
	header http.Header
	body   []byte
}

// serveOnWire answers one request through a real server, so the headers
// net/http adds are compared too. Date is removed because it changes.
func serveOnWire(t *testing.T, engine *gin.Engine, method, path string) wireResponse {
	t.Helper()
	server := httptest.NewServer(engine)
	defer server.Close()
	request, err := http.NewRequest(method, server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_ID, "u1")
	request.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_TYPE, "user")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	header := response.Header.Clone()
	header.Del("Date")
	return wireResponse{status: response.StatusCode, header: header, body: body}
}

func TestEventRoutesLookUnmountedBelowProfessional(t *testing.T) {
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionCommunity))
	t.Cleanup(entitlement.ResetForTest)
	permissions := &permissionsStub{}
	gin.SetMode(gin.TestMode)
	community := gin.New()
	enterprise := eventEngine(newTestService(permissions))
	routes := map[string]bool{}
	for _, route := range enterprise.Routes() {
		routes[route.Method+" "+route.Path] = true
	}
	for _, request := range []struct{ method, path string }{
		{http.MethodGet, "/api/bkn-backend/in/v1/knowledge-networks/kn1/event-types"},
		{http.MethodPost, "/api/bkn-backend/in/v1/knowledge-networks/kn1/events"},
		{http.MethodPost, "/api/bkn-backend/in/v1/knowledge-networks/kn1/rule-checks"},
		{http.MethodGet, "/api/bkn-backend/in/v1/knowledge-networks/kn1/event-changes"},
		{http.MethodGet, "/api/bkn-backend/v1/knowledge-networks/kn1/events"},
	} {
		if pattern := strings.Replace(request.path, "/kn1/", "/:kn_id/", 1); !routes[request.method+" "+pattern] {
			t.Fatalf("%s %s is not mounted, so the comparison proves nothing", request.method, pattern)
		}
		want := serveOnWire(t, community, request.method, request.path)
		got := serveOnWire(t, enterprise, request.method, request.path)
		if want.status != http.StatusNotFound {
			t.Fatalf("community reference status %d, want 404", want.status)
		}
		if got.status != want.status || !reflect.DeepEqual(got.header, want.header) || !reflect.DeepEqual(got.body, want.body) {
			t.Fatalf("%s %s differs from an unmounted route\ncommunity: %d %v %q\nunlicensed: %d %v %q",
				request.method, request.path, want.status, want.header, want.body, got.status, got.header, got.body)
		}
	}
	if len(permissions.checked) != 0 {
		t.Fatalf("the service ran below the tier: %v", permissions.checked)
	}
}

func TestEventRoutesServeAtProfessional(t *testing.T) {
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionProfessional))
	t.Cleanup(entitlement.ResetForTest)
	permissions := &permissionsStub{deny: true}
	engine := eventEngine(newTestService(permissions))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/bkn-backend/in/v1/knowledge-networks/kn1/events", nil)
	request.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_ID, "u1")
	request.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_TYPE, "user")
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden || len(permissions.checked) != 1 || permissions.checked[0] != "kn1:query_data" {
		t.Fatalf("status %d checked %v, want the service's 403", recorder.Code, permissions.checked)
	}
}

func TestUnlicensedNetworkDeletionRemovesEvents(t *testing.T) {
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionCommunity))
	t.Cleanup(entitlement.ResetForTest)
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
