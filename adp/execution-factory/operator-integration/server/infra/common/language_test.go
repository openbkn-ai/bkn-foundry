// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package common

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

func TestGetLanguageInfoUsesAcceptLanguage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.Header.Set(sharedrest.AcceptLanguageHeader, "en-GB")

	if got := GetLanguageInfo(c); got != AmericanEnglish {
		t.Fatalf("GetLanguageInfo() = %q, want %q", got, AmericanEnglish)
	}
}
