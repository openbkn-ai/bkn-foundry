// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package rest

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

func TestReplyErrorMarksOnlyLocallyGeneratedTextAsLocalized(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("local error", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		c.Request = c.Request.WithContext(sharedrest.WithLanguage(c.Request.Context(), sharedrest.AmericanEnglish))

		ReplyError(c, http.ErrNotSupported)

		if got := recorder.Header().Get(sharedrest.ContentLanguageHeader); got != sharedrest.AmericanEnglish {
			t.Fatalf("Content-Language = %q, want %q", got, sharedrest.AmericanEnglish)
		}
	})

	t.Run("upstream error", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		c.Request = c.Request.WithContext(sharedrest.WithLanguage(c.Request.Context(), sharedrest.AmericanEnglish))

		ReplyError(c, &ExHTTPError{HTTPCode: http.StatusBadGateway, Body: []byte(`{"message":"upstream"}`)})

		if got := recorder.Header().Get(sharedrest.ContentLanguageHeader); got != "" {
			t.Fatalf("Content-Language = %q, want empty for upstream response", got)
		}
	})
}
