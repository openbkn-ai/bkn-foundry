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
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

func TestRequireUserLocalizesAuthenticationFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, testCase := range []struct {
		name        string
		language    string
		description string
		errorLink   string
	}{
		{name: "Chinese", language: "zh-CN", description: "认证失败。", errorLink: "暂无"},
		{name: "English", language: "en-US", description: "Authentication failed.", errorLink: "None"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			router := gin.New()
			router.Use(sharedrest.LanguageMiddleware())
			router.GET("/me", sharedrest.PrivateNoCacheMiddleware(), RequireUser(nil), func(c *gin.Context) {
				c.Status(http.StatusNoContent)
			})

			request := httptest.NewRequest(http.MethodGet, "/me", nil)
			request.Header.Set(sharedrest.AcceptLanguageHeader, testCase.language)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
			}
			if got := response.Header().Get(sharedrest.ContentLanguageHeader); got != testCase.language {
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
			if got := body["error_link"]; got != testCase.errorLink {
				t.Errorf("error_link = %q, want %q when no documentation link exists", got, testCase.errorLink)
			}
		})
	}
}

func TestRouterLocalizesUnhandledPanic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := New(Deps{})
	router.GET("/panic", func(*gin.Context) { panic("test panic") })

	request := httptest.NewRequest(http.MethodGet, "/panic", nil)
	request.Header.Set(sharedrest.AcceptLanguageHeader, "en-US")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	if got := response.Header().Get(sharedrest.ContentLanguageHeader); got != "en-US" {
		t.Fatalf("Content-Language = %q, want en-US", got)
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if got := body["error_code"]; got != "BknSafe.InternalError" {
		t.Errorf("error_code = %q, want BknSafe.InternalError", got)
	}
	if got := body["description"]; got != "An internal service error occurred." {
		t.Errorf("description = %q, want English localized value", got)
	}
}

func TestHealthPanicDoesNotBecomeLocalized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := New(Deps{})
	router.GET("/health/panic", func(*gin.Context) { panic("test panic") })

	request := httptest.NewRequest(http.MethodGet, "/health/panic", nil)
	request.Header.Set(sharedrest.AcceptLanguageHeader, "en-US")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	if got := response.Header().Get(sharedrest.ContentLanguageHeader); got != "" {
		t.Errorf("Content-Language = %q, want no language header for health", got)
	}
}

func TestLocalizedErrorPreservesMachineDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(sharedrest.LanguageMiddleware())
	router.GET("/validate", func(c *gin.Context) {
		replyPublicErrorDetails(c, http.StatusBadRequest, gin.H{"field": "limit", "max": 1000})
	})

	request := httptest.NewRequest(http.MethodGet, "/validate", nil)
	request.Header.Set(sharedrest.AcceptLanguageHeader, "en-US")
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
	if body.ErrorCode != "BknSafe.InvalidRequest" || body.Description != "The request parameters are invalid." {
		t.Errorf("unexpected localized error: %#v", body)
	}
	if body.ErrorDetails["field"] != "limit" || body.ErrorDetails["max"] != float64(1000) {
		t.Errorf("error_details = %#v, want stable machine fields", body.ErrorDetails)
	}
}
