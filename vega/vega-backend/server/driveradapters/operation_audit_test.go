// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package driveradapters

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common/operationaudit"
)

func TestCaptureOperationAuditRequestRestoresOversizedBody(t *testing.T) {
	body := strings.Repeat("x", maximumOperationAuditRequestBody+1)
	request := httptest.NewRequest(http.MethodPost, "/api/vega-backend/v1/catalogs", strings.NewReader(body))
	request.ContentLength = -1 // chunked body: the middleware must restore its consumed prefix.

	if captured := captureOperationAuditRequest(request); captured != nil {
		t.Fatalf("captured oversized body = %#v, want nil", captured)
	}
	restored, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatalf("read restored request body: %v", err)
	}
	if string(restored) != body {
		t.Fatalf("request body was not restored: got %d bytes, want %d", len(restored), len(body))
	}
}

type capturedOperationAuditRecorder struct{ entries []operationaudit.Entry }

func TestNormalizeDisabledOperationAuditRecorder(t *testing.T) {
	var disabled *operationaudit.KafkaRecorder
	if normalized := normalizeOperationAuditRecorder(disabled); normalized != nil {
		t.Fatalf("disabled recorder remains a non-nil interface: %T", normalized)
	}
}

func (r *capturedOperationAuditRecorder) Record(_ context.Context, entry operationaudit.Entry) error {
	r.entries = append(r.entries, entry)
	return nil
}

func TestOperationAuditRequestIDFailureDoesNotChangeBusinessResponse(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()
	uuid.SetRand(strings.NewReader(""))
	defer uuid.SetRand(nil)

	recorder := &capturedOperationAuditRecorder{}
	handler := &restHandler{auditRecorder: recorder}
	engine := gin.New()
	engine.Use(handler.OperationAudit())
	engine.POST("/api/vega-backend/v1/catalogs", func(c *gin.Context) {
		c.Set(operationAuditVisitorKey, hydra.Visitor{ID: "user-1", Type: hydra.VisitorType("user")})
		c.Status(http.StatusCreated)
	})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/vega-backend/v1/catalogs", nil)
	engine.ServeHTTP(response, request)

	require.Equal(t, http.StatusCreated, response.Code)
	require.Empty(t, recorder.entries)
}

func TestOperationAuditRecordsBoundedManagementFact(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()
	recorder := &capturedOperationAuditRecorder{}
	handler := &restHandler{auditRecorder: recorder}
	engine := gin.New()
	engine.Use(handler.OperationAudit())
	engine.POST("/api/vega-backend/v1/catalogs", func(c *gin.Context) {
		c.Set(operationAuditVisitorKey, hydra.Visitor{ID: "user-1", Type: hydra.VisitorType("user")})
		c.Status(http.StatusCreated)
	})
	req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/v1/catalogs", strings.NewReader(`{"name":"供应链数据源","connector_config":{"password":"secret"}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("bkn-request-id", "req-a")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)
	require.Len(t, recorder.entries, 1)
	entry := recorder.entries[0]
	assert.Equal(t, "create", entry.Action)
	assert.Equal(t, "catalog", entry.TargetType)
	assert.Equal(t, "供应链数据源", entry.TargetName)
	assert.Equal(t, "success", entry.Outcome)
	assert.Equal(t, http.StatusCreated, entry.HTTPStatus)
	assert.Equal(t, []string{"connector_config", "name"}, entry.ChangedFields)
	assert.NotContains(t, entry.TargetName, "secret")
}

func TestOperationAuditReusedClientRequestIDCreatesDistinctEvents(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()
	recorder := &capturedOperationAuditRecorder{}
	handler := &restHandler{auditRecorder: recorder}
	engine := gin.New()
	engine.Use(handler.OperationAudit())
	engine.POST("/api/vega-backend/v1/catalogs", func(c *gin.Context) {
		c.Set(operationAuditVisitorKey, hydra.Visitor{ID: "user-1", Type: hydra.VisitorType("user")})
		c.Status(http.StatusCreated)
	})
	for range 2 {
		request := httptest.NewRequest(http.MethodPost, "/api/vega-backend/v1/catalogs", strings.NewReader(`{"name":"audit-test"}`))
		request.Header.Set("bkn-request-id", "reused-client-request-id")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		require.Equal(t, http.StatusCreated, response.Code)
	}
	require.Len(t, recorder.entries, 2)
	assert.NotEqual(t, recorder.entries[0].EventID, recorder.entries[1].EventID)
	assert.Equal(t, recorder.entries[0].RequestID, recorder.entries[1].RequestID)
}

func TestOperationAuditUsesCreatedResourceID(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()
	recorder := &capturedOperationAuditRecorder{}
	handler := &restHandler{auditRecorder: recorder}
	engine := gin.New()
	engine.Use(handler.OperationAudit())
	engine.POST("/api/vega-backend/v1/catalogs", func(c *gin.Context) {
		c.Set(operationAuditVisitorKey, hydra.Visitor{ID: "user-1", Type: hydra.VisitorType("user")})
		c.Set(operationAuditTargetIDKey, "created-catalog-1")
		c.Status(http.StatusCreated)
	})
	request := httptest.NewRequest(http.MethodPost, "/api/vega-backend/v1/catalogs", strings.NewReader(`{"name":"audit-e2e"}`))
	request.Header.Set("bkn-request-id", "req-create")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	require.Equal(t, http.StatusCreated, response.Code)
	require.Len(t, recorder.entries, 1)
	assert.Equal(t, "created-catalog-1", recorder.entries[0].TargetID)
}

func TestOperationAuditChangedFieldsRejectsUntrustedNames(t *testing.T) {
	fields := operationAuditChangedFields(map[string]any{
		"name": "safe", "Bearer abcdefghi": "ignored", "bkn_abcdefgh": "ignored", "bak_abcdefghijkl": "ignored",
	})
	assert.Equal(t, []string{"name"}, fields)
}

func TestOperationAuditNormalizesOversizedRequestID(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()
	recorder := &capturedOperationAuditRecorder{}
	handler := &restHandler{auditRecorder: recorder}
	engine := gin.New()
	engine.Use(handler.TraceContextMiddleware(), handler.OperationAudit())
	engine.PUT("/api/vega-backend/v1/catalogs/:id", func(c *gin.Context) {
		c.Set(operationAuditVisitorKey, hydra.Visitor{ID: "user-1", Type: hydra.VisitorType("user")})
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodPut, "/api/vega-backend/v1/catalogs/catalog-1", nil)
	request.Header.Set("bkn-request-id", "req_"+strings.Repeat("a", 128))
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	require.Equal(t, http.StatusNoContent, response.Code)
	require.Len(t, recorder.entries, 1)
	assert.LessOrEqual(t, len(recorder.entries[0].RequestID), 128)
	_, err := operationaudit.BuildKafkaAuditRecord(recorder.entries[0], "test")
	assert.NoError(t, err)
}

func TestOperationAuditNormalizesSecretLikeRequestID(t *testing.T) {
	requestID := "req_bkn_abcdefghijkl"
	alias := operationAuditCorrelationID(requestID)
	assert.NotEqual(t, requestID, alias)
	assert.Equal(t, alias, operationAuditCorrelationID(requestID))
	assert.NotContains(t, alias, "bkn_")
	entry := operationaudit.Entry{
		EventID: uuid.NewString(), EventTime: time.Now().UTC(), ActorID: "user-1",
		RequestID: alias, SourceChannel: "api", Method: "PUT", HTTPStatus: 204,
		Action: "update", TargetType: "catalog", TargetID: "catalog-1", TargetName: "catalog-1", Outcome: "success",
	}
	_, err := operationaudit.BuildKafkaAuditRecord(entry, "test")
	assert.NoError(t, err)
}

func TestOperationAuditRecordsInternalManagementActor(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()
	recorder := &capturedOperationAuditRecorder{}
	handler := &restHandler{auditRecorder: recorder}
	engine := gin.New()
	engine.Use(handler.OperationAudit())
	engine.PUT("/api/vega-backend/in/v1/catalogs/:id", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodPut, "/api/vega-backend/in/v1/catalogs/catalog-1", nil)
	request.Header.Set("x-account-id", "internal-service-1")
	request.Header.Set("x-account-type", "service")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	require.Equal(t, http.StatusNoContent, response.Code)
	require.Len(t, recorder.entries, 1)
	assert.Equal(t, "internal-service-1", recorder.entries[0].ActorID)
	assert.Equal(t, "api", recorder.entries[0].SourceChannel)
	assert.Equal(t, "internal_forwarded_header", recorder.entries[0].AuthMethod)
}

func TestOperationAuditTargetUsesBoundedBatchIdentity(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Params = gin.Params{{Key: "ids", Value: strings.Repeat("a", 180) + "," + strings.Repeat("b", 180)}}
	id, name := operationAuditTarget(context, "index_task", nil, "req-a")
	assert.LessOrEqual(t, len(id), 256)
	assert.Contains(t, id, "batch:")
	assert.Contains(t, name, "2")
}

func TestOperationAuditTargetUsesConnectorType(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Params = gin.Params{{Key: "type", Value: "mysql"}}
	id, _ := operationAuditTarget(context, "connector_type", nil, "req-a")
	assert.Equal(t, "mysql", id)
}

func TestOperationAuditTargetExcludesSecretLikeInput(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Params = gin.Params{{Key: "id", Value: "bkn_abcdefghijklm"}}
	id, name := operationAuditTarget(context, "catalog", map[string]any{"name": "Bearer abcdefghi"}, "req-a")
	assert.NotContains(t, id, "bkn_")
	assert.NotContains(t, name, "Bearer")
	entry := operationaudit.Entry{
		EventID: uuid.NewString(), EventTime: time.Now().UTC(), ActorID: "user-1",
		RequestID: "req-a", SourceChannel: "api", Method: "PUT", HTTPStatus: 204,
		Action: "update", TargetType: "catalog", TargetID: id, TargetName: name, Outcome: "success",
	}
	_, err := operationaudit.BuildKafkaAuditRecord(entry, "test")
	assert.NoError(t, err)
	context.Params = gin.Params{{Key: "id", Value: "bak_abcdefghijkl"}}
	id, name = operationAuditTarget(context, "catalog", map[string]any{"name": "bak_abcdefghijkl"}, "req-a")
	assert.NotContains(t, id, "bak_")
	assert.NotContains(t, name, "bak_")
}

func TestOperationAuditDoesNotFetchDisplayNameDuringBusinessRequest(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()
	var safeCalls atomic.Int32
	safe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		safeCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer safe.Close()
	t.Setenv("BKN_SAFE_URL", safe.URL)
	recorder := &capturedOperationAuditRecorder{}
	handler := &restHandler{auditRecorder: recorder}
	engine := gin.New()
	engine.Use(handler.OperationAudit())
	engine.PUT("/api/vega-backend/v1/catalogs/:id", func(c *gin.Context) {
		c.Set(operationAuditVisitorKey, hydra.Visitor{ID: "user-1", Type: hydra.VisitorType("user")})
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodPut, "/api/vega-backend/v1/catalogs/catalog-1", nil)
	request.Header.Set("Authorization", "Bearer test-token")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	require.Equal(t, http.StatusNoContent, response.Code)
	require.Len(t, recorder.entries, 1)
	assert.Zero(t, safeCalls.Load(), "Audit metadata must not add an external request to the business path")
}
