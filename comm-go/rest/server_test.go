// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package rest

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
)

func TestReplyOKDoesNotSetContentLanguageForMachineResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request = c.Request.WithContext(i18n.WithLanguage(c.Request.Context(), i18n.AmericanEnglish))

	ReplyOK(c, http.StatusOK, gin.H{"status": "ok"})

	if got := recorder.Header().Get(i18n.ContentLanguageHeader); got != "" {
		t.Fatalf("Content-Language = %q, want empty", got)
	}
	if got := recorder.Header().Values("Vary"); len(got) != 0 {
		t.Fatalf("Vary = %q, want none", got)
	}
}

func TestMarkLocalizedCacheableResponsePreservesExistingVaryHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Writer.Header().Set("Vary", "Origin")

	MarkLocalizedCacheableResponse(c)

	if got := recorder.Header().Values("Vary"); len(got) != 2 || got[0] != "Origin" || got[1] != i18n.AcceptLanguageHeader {
		t.Fatalf("Vary = %q, want [Origin %q]", got, i18n.AcceptLanguageHeader)
	}
}

func TestReplyErrorSetsContentLanguageWithoutVary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request = c.Request.WithContext(i18n.WithLanguage(c.Request.Context(), i18n.AmericanEnglish))

	ReplyError(c, NewHTTPError(c.Request.Context(), http.StatusBadRequest, PublicError_BadRequest))

	if got := recorder.Header().Get(i18n.ContentLanguageHeader); got != i18n.AmericanEnglish {
		t.Fatalf("Content-Language = %q, want %q", got, i18n.AmericanEnglish)
	}
	if got := recorder.Header().Values("Vary"); len(got) != 0 {
		t.Fatalf("Vary = %q, want none", got)
	}
}

func TestPrivateNoCacheMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(PrivateNoCacheMiddleware())
	router.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := recorder.Header().Get("Cache-Control"); got != "private, no-cache" {
		t.Fatalf("Cache-Control = %q, want private, no-cache", got)
	}
}
