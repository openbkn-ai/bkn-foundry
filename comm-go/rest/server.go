// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package rest

import (
	"net/http"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/gin-gonic/gin"

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
			logger.Errorf("marshal body error: %v", err)
			statusCode = http.StatusInternalServerError
			ctx := GetLanguageCtx(c)
			bodyStr = NewHTTPError(ctx, statusCode, PublicError_InternalServerError).WithErrorDetails(err).Error()
			MarkLocalizedResponse(c)
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
	var statusCode int
	var body string
	switch e := err.(type) {
	case *HTTPError:
		statusCode = e.HTTPCode
		body = e.Error()
	default:
		statusCode = http.StatusInternalServerError
		ctx := GetLanguageCtx(c)
		body = NewHTTPError(ctx, statusCode, PublicError_InternalServerError).WithErrorDetails(e.Error()).Error()
	}

	MarkLocalizedResponse(c)
	c.Writer.Header().Set(ContentTypeKey, ContentTypeJson)
	c.String(statusCode, body)
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
	c.Writer.Header().Set(ContentLanguageHeader, GetLanguageByCtx(c.Request.Context()))
}

// MarkLocalizedCacheableResponse additionally declares that a cacheable
// representation varies by Accept-Language.
func MarkLocalizedCacheableResponse(c *gin.Context) {
	MarkLocalizedResponse(c)
	addVaryHeader(c, AcceptLanguageHeader)
}

// LanguageMiddleware resolves the request locale once. It intentionally does
// not write response headers because only localized representations have a
// Content-Language value.
func LanguageMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request = c.Request.WithContext(GetLanguageCtx(c))
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
