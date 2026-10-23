// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package rest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
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
