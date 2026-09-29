// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"oss-gateway/pkg/response"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

func TestLoggerRecordsPrivateDiagnosticWithoutExposingIt(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var output bytes.Buffer
	log := logrus.New()
	log.SetOutput(&output)
	log.SetFormatter(&logrus.TextFormatter{DisableColors: true, DisableTimestamp: true})

	router := gin.New()
	router.Use(Logger(logrus.NewEntry(log)))
	router.GET("/error", func(c *gin.Context) {
		response.InternalError(c, "database connection failed")
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/error", nil))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d", http.StatusInternalServerError, recorder.Code)
	}
	if bytes.Contains(recorder.Body.Bytes(), []byte("database connection failed")) {
		t.Fatal("internal diagnostic was exposed in the response")
	}
	if !bytes.Contains(output.Bytes(), []byte("database connection failed")) {
		t.Fatalf("expected internal diagnostic in request log, got %q", output.String())
	}
}

func TestOperationLogAttributes(t *testing.T) {
	attrs := operationLogAttributes(http.MethodGet, "/api/oss-gateway/v1/health", http.StatusOK)
	values := make(map[string]string, len(attrs))
	for _, item := range attrs {
		values[string(item.Key)] = item.Value.AsString()
	}
	if values["source_id"] != "oss-gateway" || values["event_name"] != "http.request.completed" || values["outcome"] != "success" {
		t.Fatalf("unexpected product log attributes: %#v", values)
	}
	if values["log_id"] == "" || values["source_log_id"] != values["log_id"] {
		t.Fatalf("expected one non-empty source log id: %#v", values)
	}
}

func TestOperationSpanAttributesUseRouteTemplate(t *testing.T) {
	attrs := operationSpanAttributes(http.MethodPost, "/api/oss-gateway/v1/files/:id", http.StatusCreated)
	if attrs[0].Value.AsString() != http.MethodPost ||
		attrs[1].Value.AsString() != "/api/oss-gateway/v1/files/:id" ||
		attrs[2].Value.AsInt64() != http.StatusCreated {
		t.Fatalf("unexpected span attributes: %#v", attrs)
	}
}

func TestLoggerRecordsRecoveredPanicAsServerError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var output bytes.Buffer
	log := logrus.New()
	log.SetOutput(&output)
	log.SetFormatter(&logrus.TextFormatter{DisableColors: true, DisableTimestamp: true})
	router := gin.New()
	router.Use(Logger(logrus.NewEntry(log)))
	router.Use(Recovery(logrus.NewEntry(log)))
	router.GET("/panic", func(*gin.Context) { panic("boom") })
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/panic", nil))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected recovered 500, got %d", recorder.Code)
	}
	if !bytes.Contains(output.Bytes(), []byte("Server error")) {
		t.Fatalf("missing final 500 access log: %q", output.String())
	}
}
