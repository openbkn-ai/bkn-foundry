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
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/common/operationaudit"
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

func (r *capturedOperationAuditRecorder) Record(_ context.Context, entry operationaudit.Entry) error {
	r.entries = append(r.entries, entry)
	return nil
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
	assert.NotContains(t, entry.TargetName, "secret")
}
