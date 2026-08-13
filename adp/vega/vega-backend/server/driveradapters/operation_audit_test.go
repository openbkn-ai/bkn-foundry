// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package driveradapters

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-comm-go/hydra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/common/operationaudit"
	"vega-backend/interfaces"
)

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
	req.Header.Set("x-tenant-id", "tenant-a")
	req.Header.Set(interfaces.HTTP_HEADER_BUSINESS_DOMAIN, "domain-a")
	req.Header.Set("bkn-request-id", "req-a")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)
	require.Len(t, recorder.entries, 1)
	entry := recorder.entries[0]
	assert.Equal(t, "create", entry.Action)
	assert.Equal(t, "catalog", entry.TargetType)
	assert.Equal(t, "供应链数据源", entry.TargetName)
	assert.Equal(t, "tenant-a", entry.TenantID)
	assert.Equal(t, "domain-a", entry.BusinessDomainID)
	assert.Equal(t, "success", entry.Outcome)
	assert.NotContains(t, entry.TargetName, "secret")
}
