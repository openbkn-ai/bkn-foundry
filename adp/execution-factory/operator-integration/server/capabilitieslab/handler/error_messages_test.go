// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

func TestCapabilitiesLabPlatformErrorsAreLocalized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		language, code, want string
	}{
		{"zh-CN", capabilitiesLabFileRequired, "缺少必需的文件。"},
		{"en-US", capabilitiesLabFileRequired, "A required file is missing."},
		{"zh-CN", capabilitiesLabNotFound, "未找到资源。"},
		{"en-US", capabilitiesLabInvalidRequest, "Invalid request parameters."},
	} {
		t.Run(test.language+"/"+test.code, func(t *testing.T) {
			engine := gin.New()
			engine.Use(sharedrest.LanguageMiddleware(), sharedrest.PrivateNoCacheMiddleware())
			engine.GET("/test", func(c *gin.Context) { writeLocalizedError(c, http.StatusBadRequest, test.code) })
			request := httptest.NewRequest(http.MethodGet, "/test", nil)
			request.Header.Set(sharedrest.AcceptLanguageHeader, test.language)
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest || response.Header().Get(sharedrest.ContentLanguageHeader) != test.language || response.Header().Get("Vary") != sharedrest.AcceptLanguageHeader || response.Header().Get("Cache-Control") != "private, no-cache" {
				t.Fatalf("unexpected response: status=%d headers=%v", response.Code, response.Header())
			}
			var body APIErrorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Code != test.code || body.Message != test.want {
				t.Fatalf("body=%s err=%v", response.Body.String(), err)
			}
		})
	}
}

func TestCapabilitiesLabPlatformErrorEntrypointsAreLocalized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name, code, wantMessage, rawMessage string
		status                              int
		write                               func(*gin.Context)
	}{
		{
			name:        "bad request",
			code:        capabilitiesLabInvalidRequest,
			wantMessage: "Invalid request parameters.",
			rawMessage:  "field model_id is invalid",
			status:      http.StatusBadRequest,
			write: func(c *gin.Context) {
				writeBadRequest(c, "field model_id is invalid")
			},
		},
		{
			name:        "not found",
			code:        capabilitiesLabNotFound,
			wantMessage: "Resource not found.",
			rawMessage:  "capability internal-123 not found",
			status:      http.StatusNotFound,
			write: func(c *gin.Context) {
				writeNotFound(c, "capability internal-123 not found")
			},
		},
		{
			name:        "file required",
			code:        capabilitiesLabFileRequired,
			wantMessage: "A required file is missing.",
			status:      http.StatusBadRequest,
			write:       writeFileRequired,
		},
		{
			name:        "feature disabled",
			code:        capabilitiesLabFeatureDisabled,
			wantMessage: "This feature is disabled.",
			status:      http.StatusNotFound,
			write:       writeFeatureDisabled,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			engine := gin.New()
			engine.Use(sharedrest.LanguageMiddleware(), sharedrest.PrivateNoCacheMiddleware())
			engine.GET("/test", test.write)

			request := httptest.NewRequest(http.MethodGet, "/test", nil)
			request.Header.Set(sharedrest.AcceptLanguageHeader, "en-US")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)

			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
			if got := response.Header().Get(sharedrest.ContentLanguageHeader); got != "en-US" {
				t.Fatalf("Content-Language = %q, want en-US", got)
			}
			var body APIErrorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body.Code != test.code || body.Message != test.wantMessage {
				t.Fatalf("body = %#v, want code=%q message=%q", body, test.code, test.wantMessage)
			}
			if test.rawMessage != "" && strings.Contains(response.Body.String(), test.rawMessage) {
				t.Fatalf("response leaked platform diagnostic %q: %s", test.rawMessage, response.Body.String())
			}
		})
	}
}

func TestCapabilitiesLabUpstreamErrorPreservesDiagnostics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(sharedrest.LanguageMiddleware(), sharedrest.PrivateNoCacheMiddleware())
	engine.GET("/test", func(c *gin.Context) { writeBadGateway(c, "provider invalid_api_key") })
	request := httptest.NewRequest(http.MethodGet, "/test", nil)
	request.Header.Set(sharedrest.AcceptLanguageHeader, "en-US")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	var body APIErrorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Code != "upstream_error" || body.Message != "provider invalid_api_key" || response.Header().Get(sharedrest.ContentLanguageHeader) != "" {
		t.Fatalf("body=%s err=%v headers=%v", response.Body.String(), err, response.Header())
	}
}
