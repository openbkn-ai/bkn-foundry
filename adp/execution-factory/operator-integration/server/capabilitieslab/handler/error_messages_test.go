// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
