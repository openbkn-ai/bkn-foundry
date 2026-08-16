// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package rest

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
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

func TestReplyWithExecutionModeLocalizesUnexpectedSSEErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		language, description string
	}{
		{sharedrest.SimplifiedChinese, "内部错误"},
		{sharedrest.AmericanEnglish, "Internal server error"},
	}

	for _, test := range tests {
		t.Run(test.language, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			ctx := sharedrest.WithLanguage(httptest.NewRequest(http.MethodGet, "/", nil).Context(), test.language)
			ctx = common.SetExecutionModeToCtx(ctx, interfaces.ExecutionModeStream)
			ctx = common.SetStreamingModeToCtx(ctx, interfaces.StreamingModeSSE)
			c.Request = httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)

			ReplyWithExecutionMode(c, nil, errors.New("internal diagnostic"))

			if got := recorder.Header().Get(sharedrest.ContentLanguageHeader); got != test.language {
				t.Fatalf("Content-Language = %q, want %q", got, test.language)
			}
			body := recorder.Body.String()
			if !strings.Contains(body, `"code":"Public.InternalServerError"`) || !strings.Contains(body, `"description":"`+test.description+`"`) {
				t.Fatalf("SSE body = %q", body)
			}
		})
	}
}
