// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package rest

import (
	"errors"
	"net/http"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
)

// golangci-lint requires a dedicated type for context keys.

const (
	ContentTypeKey  = "Content-Type"
	ContentTypeJson = "application/json"
)

// ReplyOK writes a successful response.
func ReplyOK(c *gin.Context, statusCode int, body interface{}) {
	var (
		bodyStr string
		err     error
	)

	if body != nil {
		bodyStr, err = sonic.MarshalString(body)
		if err != nil {
			logger.Error("response JSON encoding failed")
			httpErr := NewHTTPError(i18n.GetLanguageCtx(c), http.StatusInternalServerError, PublicError_InternalServerError)
			ReplyError(c, httpErr)
			return
		}
	}

	c.Writer.Header().Set(ContentTypeKey, ContentTypeJson)
	c.String(statusCode, bodyStr)
}

func ReplyOkWithHeaders(c *gin.Context, statusCode int, body interface{}, headers map[string]string) {
	addHeaders(c, headers)
	ReplyOK(c, statusCode, body)
}

// ReplyError writes an error response.
func ReplyError(c *gin.Context, err error) {
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr == nil {
		// Preserve the original error text in the public internal-error response.
		logger.Errorf("unhandled response error: %v", err)
		httpErr = NewHTTPError(i18n.GetLanguageCtx(c), http.StatusInternalServerError, PublicError_InternalServerError)
		httpErr = httpErr.WithErrorDetails(err.Error())
	}
	renderErr := httpErr.Render()
	if renderErr != nil {
		logger.Errorf("localized error rendering failed: %v", renderErr)
	}
	body, encodeErr := sonic.Marshal(httpErr.BaseError)
	if encodeErr != nil {
		logger.Errorf("error response JSON encoding failed: error_code=%s", httpErr.BaseError.ErrorCode)
		ctx := i18n.WithLanguage(c.Request.Context(), httpErr.Lang)
		httpErr = NewHTTPError(ctx, http.StatusInternalServerError, PublicError_InternalServerError)
		renderErr = httpErr.Render()
		if renderErr != nil {
			logger.Errorf("localized error rendering failed: %v", renderErr)
		}
		// This fresh DTO contains only strings, so it cannot retain the failed data.
		body, encodeErr = sonic.Marshal(httpErr.BaseError)
		if encodeErr != nil {
			logger.Error("public error JSON encoding failed")
			body = []byte(`{"error_code":"Public.InternalServerError","description":"","error_details":""}`)
		}
	}
	c.Writer.Header().Set(i18n.ContentLanguageHeader, string(httpErr.Lang))
	c.Writer.Header().Set(ContentTypeKey, ContentTypeJson)
	c.String(httpErr.HTTPCode, string(body))
}

func ReplyErrorWithHeaders(c *gin.Context, err error, headers map[string]string) {
	addHeaders(c, headers)
	ReplyError(c, err)
}

func addHeaders(c *gin.Context, headers map[string]string) {
	if len(headers) > 0 {
		for k, v := range headers {
			c.Writer.Header().Set(k, v)
		}
	}
}

// MarkLocalizedResponse records the language used by a response that contains
// localized text. Callers opt in because a successful response can be purely
// machine-readable even when the request has an effective locale.
func MarkLocalizedResponse(c *gin.Context) {
	c.Writer.Header().Set(i18n.ContentLanguageHeader, string(i18n.GetLanguageByCtx(c.Request.Context())))
}

// MarkLocalizedCacheableResponse additionally declares that a cacheable
// representation varies by Accept-Language.
func MarkLocalizedCacheableResponse(c *gin.Context) {
	MarkLocalizedResponse(c)
	addVaryHeader(c, i18n.AcceptLanguageHeader)
}

// LanguageMiddleware resolves the request locale once. It intentionally does
// not write response headers because only localized representations have a
// Content-Language value.
func LanguageMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request = c.Request.WithContext(i18n.GetLanguageCtx(c))
		c.Next()
	}
}

// PrivateNoCacheMiddleware prevents shared caches from storing authenticated
// responses and requires a private cache to revalidate before reuse.
func PrivateNoCacheMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Cache-Control", "private, no-cache")
		c.Next()
	}
}

func addVaryHeader(c *gin.Context, value string) {
	values := c.Writer.Header().Values("Vary")
	for _, headerValue := range values {
		for _, token := range strings.Split(headerValue, ",") {
			token = strings.TrimSpace(token)
			if token == "*" || strings.EqualFold(token, value) {
				return
			}
		}
	}
	c.Writer.Header().Add("Vary", value)
}
