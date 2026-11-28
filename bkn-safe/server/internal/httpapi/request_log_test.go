// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

// captureRequestLog swaps the default logger for one that records JSON lines.
func captureRequestLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &output
}

// requestLogLines returns the "safe request failed" lines written so far.
func requestLogLines(t *testing.T, output *bytes.Buffer) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		if raw == "" {
			continue
		}
		var line map[string]any
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("decode log line %q: %v", raw, err)
		}
		if line["msg"] == "safe request failed" {
			lines = append(lines, line)
		}
	}
	return lines
}

func onlyRequestLogLine(t *testing.T, output *bytes.Buffer) map[string]any {
	t.Helper()
	lines := requestLogLines(t, output)
	if len(lines) != 1 {
		t.Fatalf("request log lines = %d, want 1: %s", len(lines), output.String())
	}
	return lines[0]
}

func postAuthz(r *gin.Engine, path, requestID string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(body)
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-request-id", requestID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// Regression for #3449: a resource-filter engine failure answered 500 and left
// no record of why, so callers' 500s could not be traced to a cause.
func TestResourceFilterFailureLogsItsCause(t *testing.T) {
	r, _, db := newTestServer(t)
	seedEnabledUser(t, db, "u-1")
	seedCatalogOps(t, db, "knowledge_network", "view_detail")
	if err := db.Migrator().DropTable(&model.Operation{}); err != nil {
		t.Fatal(err)
	}
	output := captureRequestLog(t)

	w := postAuthz(r, "/api/safe/v1/authz/resource-filter", "req-filter-failure", map[string]any{
		"accessor_id":        "u-1",
		"resource_type":      "knowledge_network",
		"resource_ids":       []string{"kn-1"},
		"include_operations": true,
	})
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("resource-filter = %d, want 500: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "no such table") {
		t.Fatalf("response leaks the cause: %s", w.Body.String())
	}
	line := onlyRequestLogLine(t, output)
	for key, want := range map[string]any{
		"level":      "ERROR",
		"method":     http.MethodPost,
		"route":      "/api/safe/v1/authz/resource-filter",
		"status":     float64(http.StatusInternalServerError),
		"request_id": "req-filter-failure",
	} {
		if line[key] != want {
			t.Errorf("%s = %v, want %v", key, line[key], want)
		}
	}
	if cause, _ := line["err"].(string); !strings.Contains(cause, "no such table") {
		t.Errorf("err = %q, want the engine failure", cause)
	}
	if _, ok := line["duration"].(float64); !ok {
		t.Errorf("duration = %v, want a number", line["duration"])
	}
}

// The account lookup failing is the other way resource-filter fails fast; its
// 503 must name the cause too.
func TestAuthzUnavailableLogsItsCause(t *testing.T) {
	r, _, db := newTestServer(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	output := captureRequestLog(t)

	w := postAuthz(r, "/api/safe/v1/authz/resource-filter", "req-account-failure", map[string]any{
		"accessor_id":   "u-1",
		"resource_type": "knowledge_network",
		"resource_ids":  []string{"kn-1"},
	})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("resource-filter = %d, want 503: %s", w.Code, w.Body.String())
	}
	line := onlyRequestLogLine(t, output)
	if line["level"] != "ERROR" || line["status"] != float64(http.StatusServiceUnavailable) {
		t.Errorf("level/status = %v/%v, want ERROR/503", line["level"], line["status"])
	}
	if cause, _ := line["err"].(string); !strings.Contains(cause, "database is closed") {
		t.Errorf("err = %q, want the account store failure", cause)
	}
}

func TestRequestLogSkipsSuccessAndNonAuthzClientErrors(t *testing.T) {
	r, _, db := newTestServer(t)
	seedEnabledUser(t, db, "u-1")
	output := captureRequestLog(t)

	if w := postAuthz(r, "/api/safe/v1/authz/resource-filter", "req-ok", map[string]any{
		"accessor_id": "u-1", "resource_type": "knowledge_network", "resource_ids": []string{"kn-1"},
	}); w.Code != http.StatusOK {
		t.Fatalf("resource-filter = %d, want 200: %s", w.Code, w.Body.String())
	}
	if w := postAuthz(r, "/api/safe/v1/unknown", "req-elsewhere", map[string]any{}); w.Code != http.StatusNotFound {
		t.Fatalf("unknown route = %d, want 404", w.Code)
	}
	if lines := requestLogLines(t, output); len(lines) != 0 {
		t.Fatalf("request log lines = %d, want 0: %s", len(lines), output.String())
	}
}

func TestRequestLogWarnsOnAuthzClientErrors(t *testing.T) {
	r, _, _ := newTestServer(t)
	output := captureRequestLog(t)

	w := postAuthz(r, "/api/safe/v1/authz/resource-filter", "req-bad", map[string]any{
		"accessor_id": "u-1", "resource_ids": []string{"kn-1"},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("resource-filter = %d, want 400: %s", w.Code, w.Body.String())
	}
	line := onlyRequestLogLine(t, output)
	if line["level"] != "WARN" || line["status"] != float64(http.StatusBadRequest) || line["request_id"] != "req-bad" {
		t.Errorf("line = %v, want a WARN 400 for req-bad", line)
	}
}

// A sibling read the caller cancelled is not a bkn-safe fault: it is logged,
// but at warning, and says the request context ended.
func TestRequestLogWarnsWhenCallerCancelled(t *testing.T) {
	r, _, _ := newTestServer(t)
	output := captureRequestLog(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(map[string]any{
		"accessor_id": "u-1", "resource_type": "knowledge_network", "resource_ids": []string{"kn-1"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/safe/v1/authz/resource-filter", &buf).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("resource-filter = %d, want 503: %s", w.Code, w.Body.String())
	}
	line := onlyRequestLogLine(t, output)
	if line["level"] != "WARN" || line["request_ctx"] != context.Canceled.Error() {
		t.Errorf("line = %v, want WARN with request_ctx=%q", line, context.Canceled.Error())
	}
}

func TestRequestLogRecordsRecoveredPanic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := New(Deps{})
	router.GET("/panic", func(*gin.Context) { panic("test panic") })
	output := captureRequestLog(t)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/panic", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("panic = %d, want 500", w.Code)
	}
	line := onlyRequestLogLine(t, output)
	if line["level"] != "ERROR" || line["route"] != "/panic" {
		t.Errorf("line = %v, want ERROR for /panic", line)
	}
}
