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
