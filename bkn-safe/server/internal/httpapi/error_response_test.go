// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/httperrors"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/proxygrant"
)

func TestRequireUserLocalizesAuthenticationFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, testCase := range []struct {
		name        string
		language    string
		description string
	}{
		{name: "Chinese", language: "zh-CN", description: "认证失败。请提供有效的认证凭据。"},
		{name: "English", language: "en-US", description: "Authentication failed. Provide valid authentication credentials."},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			router := gin.New()
			router.Use(rest.LanguageMiddleware())
			router.GET("/me", rest.PrivateNoCacheMiddleware(), RequireUser(nil), func(c *gin.Context) {
				c.Status(http.StatusNoContent)
			})

			request := httptest.NewRequest(http.MethodGet, "/me", nil)
			request.Header.Set(i18n.AcceptLanguageHeader, testCase.language)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
			}
			if got := response.Header().Get(i18n.ContentLanguageHeader); got != testCase.language {
				t.Fatalf("Content-Language = %q, want %q", got, testCase.language)
			}
			if got := response.Header().Get("Cache-Control"); got != "private, no-cache" {
				t.Fatalf("Cache-Control = %q, want private, no-cache", got)
			}
			var body map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if got := body["error_code"]; got != "BknSafe.Unauthorized" {
				t.Errorf("error_code = %q, want BknSafe.Unauthorized", got)
			}
			if got := body["description"]; got != testCase.description {
				t.Errorf("description = %q, want %q", got, testCase.description)
			}
			if got := body["error"]; got != testCase.description {
				t.Errorf("legacy error = %q, want localized description %q", got, testCase.description)
			}
			for _, removed := range []string{"solution", "error_link"} {
				if _, exists := body[removed]; exists {
					t.Errorf("removed field %s is still present", removed)
				}
			}
		})
	}
}

func TestRouterLocalizesUnhandledPanic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := New(Deps{})
	router.GET("/panic", func(*gin.Context) { panic("test panic") })

	request := httptest.NewRequest(http.MethodGet, "/panic", nil)
	request.Header.Set(i18n.AcceptLanguageHeader, "en-US")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	if got := response.Header().Get(i18n.ContentLanguageHeader); got != "en-US" {
		t.Fatalf("Content-Language = %q, want en-US", got)
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if got := body["error_code"]; got != "BknSafe.InternalError" {
		t.Errorf("error_code = %q, want BknSafe.InternalError", got)
	}
	if got := body["description"]; got != "An internal service error occurred. Try again later; contact an administrator if the problem persists." {
		t.Errorf("description = %q, want English localized value", got)
	}
}

func TestHealthPanicDoesNotBecomeLocalized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := New(Deps{})
	router.GET("/health/panic", func(*gin.Context) { panic("test panic") })

	request := httptest.NewRequest(http.MethodGet, "/health/panic", nil)
	request.Header.Set(i18n.AcceptLanguageHeader, "en-US")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	if got := response.Header().Get(i18n.ContentLanguageHeader); got != "" {
		t.Errorf("Content-Language = %q, want no language header for health", got)
	}
}

func TestLocalizedErrorPreservesMachineDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(rest.LanguageMiddleware())
	router.GET("/validate", func(c *gin.Context) {
		replyPublicErrorDetails(c, http.StatusBadRequest, gin.H{"field": "limit", "max": 1000})
	})

	request := httptest.NewRequest(http.MethodGet, "/validate", nil)
	request.Header.Set(i18n.AcceptLanguageHeader, "en-US")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	var body struct {
		ErrorCode    string         `json:"error_code"`
		Description  string         `json:"description"`
		ErrorDetails map[string]any `json:"error_details"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.ErrorCode != "BknSafe.InvalidRequest" || body.Description != "The request parameters are invalid. Check the request parameters." {
		t.Errorf("unexpected localized error: %#v", body)
	}
	if body.ErrorDetails["field"] != "limit" || body.ErrorDetails["max"] != float64(1000) {
		t.Errorf("error_details = %#v, want stable machine fields", body.ErrorDetails)
	}
}

func TestProxyGrantFenceConflictsExposeStableCodes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name string
		err  error
		code string
	}{
		{name: "stale generation", err: proxygrant.ErrStaleSync, code: httperrors.ProxyGrantStaleSync},
		{name: "snapshot conflict", err: proxygrant.ErrSnapshotConflict, code: httperrors.ProxyGrantSnapshotConflict},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			router.Use(rest.LanguageMiddleware())
			router.GET("/proxy-error", func(c *gin.Context) { writeProxyGrantError(c, test.err) })
			request := httptest.NewRequest(http.MethodGet, "/proxy-error", nil)
			request.Header.Set(i18n.AcceptLanguageHeader, "en-US")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			var body map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusConflict || body["error_code"] != test.code {
				t.Fatalf("response = %d %#v, want 409 %s", response.Code, body, test.code)
			}
		})
	}
}
