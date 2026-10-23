// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package rest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

func TestReplyErrorMarksOnlyLocallyGeneratedTextAsLocalized(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("local error", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		c.Request = c.Request.WithContext(i18n.WithLanguage(c.Request.Context(), i18n.AmericanEnglish))

		ReplyError(c, http.ErrNotSupported)

		if got := recorder.Header().Get(i18n.ContentLanguageHeader); got != string(i18n.AmericanEnglish) {
			t.Fatalf("Content-Language = %q, want %q", got, i18n.AmericanEnglish)
		}
	})

	t.Run("upstream error", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		c.Request = c.Request.WithContext(i18n.WithLanguage(c.Request.Context(), i18n.AmericanEnglish))

		ReplyError(c, &ExHTTPError{HTTPCode: http.StatusBadGateway, Body: []byte(`{"message":"upstream"}`)})

		if got := recorder.Header().Get(i18n.ContentLanguageHeader); got != "" {
			t.Fatalf("Content-Language = %q, want empty for upstream response", got)
		}
	})
}

func TestReplyWithExecutionModeLocalizesUnexpectedSSEErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		language    i18n.Language
		description string
	}{
		{i18n.SimplifiedChinese, "服务内部错误。请稍后重试。"},
		{i18n.AmericanEnglish, "An internal server error occurred. Try again later."},
	}

	for _, test := range tests {
		t.Run(string(test.language), func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			ctx := i18n.WithLanguage(httptest.NewRequest(http.MethodGet, "/", nil).Context(), test.language)
			ctx = common.SetExecutionModeToCtx(ctx, interfaces.ExecutionModeStream)
			ctx = common.SetStreamingModeToCtx(ctx, interfaces.StreamingModeSSE)
			c.Request = httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)

			ReplyWithExecutionMode(c, nil, errors.New("internal diagnostic"))

			if got := recorder.Header().Get(i18n.ContentLanguageHeader); got != string(test.language) {
				t.Fatalf("Content-Language = %q, want %q", got, test.language)
			}
			body := recorder.Body.String()
			if !strings.Contains(body, `"error_code":"Public.InternalServerError"`) || !strings.Contains(body, `"description":"`+test.description+`"`) {
				t.Fatalf("SSE body = %q", body)
			}
		})
	}
}

// Response language follows the constructed error, even when the request differs.
func TestReplyErrorUnwrapsSharedError(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil).WithContext(i18n.WithLanguage(context.Background(), i18n.SimplifiedChinese))
	public := rest.NewHTTPError(i18n.WithLanguage(context.Background(), i18n.AmericanEnglish), http.StatusForbidden, rest.PublicError_Forbidden).WithErrorDetails("request denied")
	ReplyError(c, fmt.Errorf("operation failed: %w", public))
	if recorder.Code != http.StatusForbidden || recorder.Header().Get(i18n.ContentLanguageHeader) != string(i18n.AmericanEnglish) {
		t.Fatalf("status/header = %d %v", recorder.Code, recorder.Header())
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["error_code"] != rest.PublicError_Forbidden || body["description"] == "" || body["error_details"] != "request denied" {
		t.Fatalf("body = %s", recorder.Body.String())
	}
	for _, legacy := range []string{"code", "details", "solution", "link"} {
		if _, exists := body[legacy]; exists {
			t.Fatalf("legacy field %s present", legacy)
		}
	}
}

func TestReplyErrorForUnmappedValidationTag(t *testing.T) {
	validate := validator.New()
	err := validate.Struct(struct {
		Value string `validate:"contains=foo"`
	}{Value: "bar"})
	if err == nil {
		t.Fatal("expected validation failure")
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	ReplyError(c, err)
	var body rest.BaseError
	if decodeErr := json.Unmarshal(recorder.Body.Bytes(), &body); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if recorder.Code != http.StatusBadRequest || body.ErrorCode != rest.PublicError_BadRequest || body.Description == "" {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
}
