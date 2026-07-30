// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package oteltrace

import (
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-comm-go/rest"
	attr "go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	KEY_HTTP_URL                    = "http.url"
	KEY_HTTP_METHOD                 = "http.method"
	KEY_HTTP_HEADER_METHOD_OVERRIDE = "http.header.X-Http-Method-Override"
	KEY_HTTP_HEADER_X_LANGUAGE      = "http.header.X-Language"
	KEY_HTTP_HEADER_CONTENT_TYPE    = "http.header.Content-Type"
	KEY_HTTP_HEADER_USER_AGENT      = "http.header.User-Agent"
	KEY_HTTP_HEADER_ACCOUNT_ID      = "http.header.X-Account-ID"
	KEY_HTTP_HEADER_ACCOUNT_TYPE    = "http.header.X-Account-Type"
	KEY_HTTP_STATUS                 = "http.status"
	KEY_HTTP_ERROR_CODE             = "http.error_code"
	KEY_HTTP_ROUTE                  = "http.route"
	KEY_HTTP_CLIENT_IP              = "http.client_ip"

	CONTENT_TYPE_NAME = "Content-Type"
	CONTENT_TYPE_JSON = "application/json"

	HTTP_HEADER_USER_AGENT      = "User-Agent"
	HTTP_HEADER_FORWARDED_FOR   = "X-Forwarded-For"
	HTTP_HEADER_METHOD_OVERRIDE = "X-Http-Method-Override"
	HTTP_HEADER_X_LANGUAGE      = "X-Language"
	HTTP_HEADER_ACCOUNT_ID      = "x-account-id"
	HTTP_HEADER_ACCOUNT_TYPE    = "x-account-type"
)

// TraceAttrs HTTP 请求相关属性的汇总，用于埋点。
type TraceAttrs struct {
	HttpUrl            string
	HttpMethod         string
	HttpMethodOverride string
	HttpXLanguage      string
	HttpContentType    string
	HttpUserAgent      string
	HttpAccountID      string
	HttpAccountType    string
	HttpRoute          string
	HttpClientIP       string
}

// GetAttrsByGinCtx 从 *gin.Context 抽取 TraceAttrs。
func GetAttrsByGinCtx(c *gin.Context) TraceAttrs {
	return TraceAttrs{
		HttpUrl:            fmt.Sprintf("http://%s%s", c.Request.Host, c.Request.RequestURI),
		HttpMethod:         c.Request.Method,
		HttpContentType:    c.GetHeader(CONTENT_TYPE_NAME),
		HttpMethodOverride: c.GetHeader(HTTP_HEADER_METHOD_OVERRIDE),
		HttpXLanguage:      c.GetHeader(HTTP_HEADER_X_LANGUAGE),
		HttpUserAgent:      c.GetHeader(HTTP_HEADER_USER_AGENT),
		HttpAccountID:      c.GetHeader(HTTP_HEADER_ACCOUNT_ID),
		HttpAccountType:    c.GetHeader(HTTP_HEADER_ACCOUNT_TYPE),
		HttpRoute:          c.FullPath(),
		HttpClientIP:       serverClientIP(c.GetHeader(HTTP_HEADER_FORWARDED_FOR)),
	}
}

// serverClientIP 从 X-Forwarded-For 头取第一段。
func serverClientIP(xForwardedFor string) string {
	if idx := strings.Index(xForwardedFor, ","); idx >= 0 {
		xForwardedFor = xForwardedFor[:idx]
	}
	return xForwardedFor
}

// AddHttpAttrs4API 设置 API 入口 span 的 HTTP 属性。
func AddHttpAttrs4API(span trace.Span, attrs TraceAttrs) {
	span.SetAttributes(
		attr.Key(KEY_HTTP_URL).String(attrs.HttpUrl),
		attr.Key(KEY_HTTP_METHOD).String(attrs.HttpMethod),
		attr.Key(KEY_HTTP_HEADER_CONTENT_TYPE).String(attrs.HttpContentType),
		attr.Key(KEY_HTTP_HEADER_X_LANGUAGE).String(attrs.HttpXLanguage),
		attr.Key(KEY_HTTP_HEADER_USER_AGENT).String(attrs.HttpUserAgent),
		attr.Key(KEY_HTTP_HEADER_ACCOUNT_ID).String(attrs.HttpAccountID),
		attr.Key(KEY_HTTP_HEADER_ACCOUNT_TYPE).String(attrs.HttpAccountType),
		attr.Key(KEY_HTTP_ROUTE).String(attrs.HttpRoute),
		attr.Key(KEY_HTTP_CLIENT_IP).String(attrs.HttpClientIP),
	)
	if attrs.HttpMethodOverride != "" {
		span.SetAttributes(
			attr.Key(KEY_HTTP_HEADER_METHOD_OVERRIDE).String(attrs.HttpMethodOverride),
		)
	}
}

// AddHttpAttrs4Error 设置错误状态到 span。
func AddHttpAttrs4Error(span trace.Span, status int, errorCode string, statusDescription string) {
	span.SetAttributes(
		attr.Key(KEY_HTTP_STATUS).Int(status),
		attr.Key(KEY_HTTP_ERROR_CODE).String(errorCode),
	)
	span.SetStatus(codes.Error, statusDescription)
}

// AddHttpAttrs4HttpError 从 *rest.HTTPError 设置错误到 span。
func AddHttpAttrs4HttpError(span trace.Span, err *rest.HTTPError) {
	span.SetAttributes(
		attr.Key(KEY_HTTP_STATUS).Int(err.HTTPCode),
		attr.Key(KEY_HTTP_ERROR_CODE).String(err.BaseError.ErrorCode),
	)
	span.SetStatus(codes.Error, fmt.Sprintf("%v", err.BaseError.ErrorDetails))
}

// AddHttpAttrs4Ok 设置成功状态到 span。
func AddHttpAttrs4Ok(span trace.Span, status int) {
	span.SetAttributes(attr.Key(KEY_HTTP_STATUS).Int(status))
	span.SetStatus(codes.Ok, "")
}

// AddAttrs4InternalHttp 设置内部 http 调用 span 的属性。
func AddAttrs4InternalHttp(span trace.Span, attrs TraceAttrs) {
	span.SetAttributes(
		attr.Key(KEY_HTTP_URL).String(attrs.HttpUrl),
		attr.Key(KEY_HTTP_METHOD).String(attrs.HttpMethod),
		attr.Key(KEY_HTTP_HEADER_CONTENT_TYPE).String(attrs.HttpContentType),
	)
	if attrs.HttpMethodOverride != "" {
		span.SetAttributes(attr.Key(KEY_HTTP_HEADER_METHOD_OVERRIDE).String(attrs.HttpMethodOverride))
	}
}
