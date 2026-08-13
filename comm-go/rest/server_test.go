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
)

func TestReplyOKSetsContentLanguage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request = c.Request.WithContext(WithLanguage(c.Request.Context(), AmericanEnglish))

	ReplyOK(c, http.StatusOK, gin.H{"status": "ok"})

	if got := recorder.Header().Get(ContentLanguageHeader); got != AmericanEnglish {
		t.Fatalf("Content-Language = %q, want %q", got, AmericanEnglish)
	}
	if got := recorder.Header().Values("Vary"); len(got) != 1 || got[0] != AcceptLanguageHeader {
		t.Fatalf("Vary = %q, want [%q]", got, AcceptLanguageHeader)
	}
}

func TestReplyOKPreservesExistingVaryHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Writer.Header().Set("Vary", "Origin")

	ReplyOK(c, http.StatusOK, gin.H{"status": "ok"})

	if got := recorder.Header().Values("Vary"); len(got) != 2 || got[0] != "Origin" || got[1] != AcceptLanguageHeader {
		t.Fatalf("Vary = %q, want [Origin %q]", got, AcceptLanguageHeader)
	}
}
