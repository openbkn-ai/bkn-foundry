// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package rest

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
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

	ReplyError(c, httperrors.NewHTTPError(c.Request.Context(), http.StatusBadRequest, httperrors.PublicError_BadRequest))

	if got := recorder.Header().Get(i18n.ContentLanguageHeader); got != string(i18n.AmericanEnglish) {
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

func TestReplyErrorRendersCompleteParameters(t *testing.T) {
	code := "Test.Response"
	loadErrorCatalog(t, code, "参数 {{ .field }}", "Field {{ .field }}")
	registerTestCode(t, code)
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		lang        i18n.Language
		description string
	}{
		{i18n.SimplifiedChinese, "参数 title"}, {i18n.AmericanEnglish, "Field title"},
	} {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		ctx.Request = ctx.Request.WithContext(i18n.WithLanguage(ctx.Request.Context(), tc.lang))
		err := httperrors.NewHTTPError(ctx.Request.Context(), 400, code).WithMetadata(map[string]any{"field": "title"})
		ReplyError(ctx, fmt.Errorf("wrapped: %w", err))
		if recorder.Code != 400 || recorder.Header().Get(i18n.ContentLanguageHeader) != string(tc.lang) {
			t.Fatalf("response: %#v", recorder)
		}
		assertErrorBody(t, recorder.Body.Bytes(), code, tc.description)
	}
}

func TestRenderingFailurePreservesBusinessResponse(t *testing.T) {
	code := "Test.Failure"
	loadErrorCatalog(t, code, "prefix {{ index .items 4 }}", "prefix {{ index .items 4 }}")
	registerTestCode(t, code)
	err := httperrors.NewHTTPError(i18n.WithLanguage(t.Context(), i18n.AmericanEnglish), 409, code).WithMetadata(map[string]any{"items": []string{"public"}})
	err.Description = "stale description"
	renderErr := err.Render()
	var failure *i18n.RenderError
	if !errors.As(renderErr, &failure) || failure.Kind != i18n.TemplateExecution {
		t.Fatalf("render failure = %v", renderErr)
	}
	if err.Description != "" {
		t.Fatalf("render = %#v", err.BaseError)
	}
	assertErrorBody(t, []byte(err.Error()), code, "")
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	ReplyError(ctx, err)
	if recorder.Code != 409 {
		t.Fatalf("status = %d", recorder.Code)
	}
	assertErrorBody(t, recorder.Body.Bytes(), code, "")

	// Manually constructed errors can bypass startup registration.
	err = &httperrors.HTTPError{HTTPCode: 409, Lang: i18n.AmericanEnglish, BaseError: httperrors.BaseError{ErrorCode: "Test.Missing"}}
	renderErr = err.Render()
	if !errors.As(renderErr, &failure) || failure.Kind != i18n.MessageMissing {
		t.Fatalf("missing message = %v", renderErr)
	}
	assertErrorBody(t, []byte(err.Error()), "Test.Missing", "")
}

func loadErrorCatalog(t *testing.T, code, zhDescription, enDescription string) {
	t.Helper()
	i18n.RegisterI18n(&i18n.I18nMessages{Locale: i18n.SimplifiedChinese, Messages: map[string]string{code: zhDescription}})
	i18n.RegisterI18n(&i18n.I18nMessages{Locale: i18n.AmericanEnglish, Messages: map[string]string{code: enDescription}})
}

func registerTestCode(t *testing.T, code string) {
	t.Helper()
	httperrors.Register([]string{code})
}

func assertErrorBody(t *testing.T, data []byte, code, description string) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("decode response %q: %v", data, err)
	}
	if body["error_code"] != code || body["description"] != description {
		t.Fatalf("response = %s", data)
	}
	for _, internal := range []string{"solution", "error_link", "HTTPCode", "Language", "BaseError", "DescriptionTemplateData", "SolutionTemplateData", "language"} {
		if _, exists := body[internal]; exists {
			t.Fatalf("response exposes %s: %s", internal, data)
		}
	}
	return body
}

func TestErrorResponseUsesConstructionLanguage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("GET", "/", nil).WithContext(i18n.WithLanguage(t.Context(), i18n.SimplifiedChinese))
	err := httperrors.NewHTTPError(i18n.WithLanguage(t.Context(), i18n.AmericanEnglish), 400, httperrors.PublicError_BadRequest)
	ReplyError(c, fmt.Errorf("wrapped: %w", err))
	if rec.Code != 400 || rec.Header().Get(i18n.ContentLanguageHeader) != string(i18n.AmericanEnglish) {
		t.Fatalf("status=%d language=%s", rec.Code, rec.Header().Get(i18n.ContentLanguageHeader))
	}
	var body map[string]any
	if decodeErr := json.Unmarshal(rec.Body.Bytes(), &body); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if body["error_code"] != httperrors.PublicError_BadRequest || !strings.Contains(body["description"].(string), "request") {
		t.Fatalf("body=%s", rec.Body)
	}
}

func TestFallbackErrorResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name    string
		reply   func(*gin.Context)
		details string
	}{
		{"ordinary error preserves text", func(c *gin.Context) { ReplyError(c, errors.New("operation failed")) }, "operation failed"},
		{"success response encoding failure", func(c *gin.Context) { ReplyOK(c, 200, make(chan int)) }, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("GET", "/", nil)
			tc.reply(c)
			if rec.Code != 500 {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
			}
			var body httperrors.BaseError
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.ErrorCode != httperrors.PublicError_InternalServerError || body.ErrorDetails != tc.details || body.Description == "" {
				t.Fatalf("body=%s", rec.Body)
			}
		})
	}
}

func TestEncodingFailureReturnsCodeAndSafeInternalResponse(t *testing.T) {
	e := httperrors.NewHTTPError(i18n.WithLanguage(t.Context(), i18n.AmericanEnglish), 400, httperrors.PublicError_BadRequest).
		WithMetadata(map[string]any{"private": make(chan int)}).WithErrorDetails("{{ literal }}")
	if _, err := json.Marshal(e); err == nil {
		t.Fatal("invalid JSON value was accepted")
	}
	if e.Error() != httperrors.PublicError_BadRequest || e.BaseError.Error() != httperrors.PublicError_BadRequest {
		t.Fatal("encoding failure lost the stable code")
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("GET", "/", nil)
	ReplyError(c, e)
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 500 || body["error_code"] != httperrors.PublicError_InternalServerError || body["metadata"] != nil || body["error_details"] != "" || rec.Header().Get(i18n.ContentLanguageHeader) != string(i18n.AmericanEnglish) {
		t.Fatalf("unsafe encoding-failure response: %d %s", rec.Code, rec.Body)
	}
	if e.ErrorDetails != "{{ literal }}" || len(e.Metadata) != 1 {
		t.Fatal("encoding failure mutated original error")
	}
}
