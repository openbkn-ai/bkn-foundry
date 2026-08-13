// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package driveradapters

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
)

type traceOutboxAuthStub struct {
	visitor hydra.Visitor
	err     error
}

func (s traceOutboxAuthStub) VerifyToken(context.Context, *gin.Context) (hydra.Visitor, error) {
	return s.visitor, s.err
}

func TestListTraceOutboxAuthorization(t *testing.T) {
	previousMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	defer gin.SetMode(previousMode)

	t.Run("OAuth failure returns unauthorized", func(t *testing.T) {
		handler := &restHandler{as: traceOutboxAuthStub{err: errors.New("invalid token")}}
		if status := traceOutboxListStatus(handler); status != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", status, http.StatusUnauthorized)
		}
	})

	t.Run("non admin role returns forbidden", func(t *testing.T) {
		server := traceOutboxSafeServer(t, http.StatusOK, []string{"normal_user"})
		defer server.Close()
		t.Setenv("BKN_SAFE_BASE_URL", server.URL)
		t.Setenv("BKN_SAFE_URL", "")
		handler := &restHandler{as: traceOutboxAuthStub{visitor: hydra.Visitor{ID: "operator-1"}}}
		if status := traceOutboxListStatus(handler); status != http.StatusForbidden {
			t.Fatalf("status = %d, want %d", status, http.StatusForbidden)
		}
	})

	t.Run("BKN Safe failure returns service unavailable", func(t *testing.T) {
		server := traceOutboxSafeServer(t, http.StatusInternalServerError, nil)
		defer server.Close()
		t.Setenv("BKN_SAFE_BASE_URL", server.URL)
		t.Setenv("BKN_SAFE_URL", "")
		handler := &restHandler{as: traceOutboxAuthStub{visitor: hydra.Visitor{ID: "operator-1"}}}
		if status := traceOutboxListStatus(handler); status != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want %d", status, http.StatusServiceUnavailable)
		}
	})

	t.Run("disabled outbox returns service unavailable", func(t *testing.T) {
		server := traceOutboxSafeServer(t, http.StatusOK, []string{"admin"})
		defer server.Close()
		t.Setenv("BKN_SAFE_BASE_URL", server.URL)
		t.Setenv("BKN_SAFE_URL", "")
		handler := &restHandler{as: traceOutboxAuthStub{visitor: hydra.Visitor{ID: "operator-1"}}}
		if status := traceOutboxListStatus(handler); status != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want %d", status, http.StatusServiceUnavailable)
		}
	})
}

func traceOutboxListStatus(handler *restHandler) int {
	writer := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(writer)
	context.Request = httptest.NewRequest(http.MethodGet, "/trace/outbox", nil)
	context.Request.Header.Set("Authorization", "Bearer test-token")
	handler.ListTraceOutbox(context)
	return writer.Code
}

func traceOutboxSafeServer(t *testing.T, status int, roles []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if status != http.StatusOK {
			writer.WriteHeader(status)
			return
		}
		switch request.URL.Path {
		case "/api/safe/v1/me":
			_, _ = writer.Write([]byte(`{"id":"operator-1","enabled":true,"roles":["` + joinRoles(roles) + `"]}`))
		case "/api/safe/v1/me/permissions":
			_, _ = writer.Write([]byte(`{"permissions":[]}`))
		default:
			http.NotFound(writer, request)
		}
	}))
}

func joinRoles(roles []string) string {
	if len(roles) == 0 {
		return ""
	}
	return roles[0]
}
