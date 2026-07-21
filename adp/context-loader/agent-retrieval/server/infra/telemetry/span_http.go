// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/openbkn-ai/bkn-comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

var clearUserPassRe = regexp.MustCompile(`(://)[^/]*@`)

const (
	// maxHeaderLogSize 请求头日志记录最大字节数
	maxHeaderLogSize = 4096
)

// HTTPRequest 发起HTTP请求
func HTTPRequest(ctx context.Context, req *http.Request, fn func(req *http.Request) (*http.Response, error)) (rsp *http.Response, err error) {
	tracer := otel.GetTracerProvider()
	if tracer != nil {
		var span trace.Span
		ctx, span = oteltrace.StartNamedClientSpan(ctx, "http.request")
		arr := strings.Split(req.Proto, "/")
		span.SetAttributes(attribute.Key("net.protocol.name").String(arr[0]))
		if len(arr) > 1 {
			span.SetAttributes(attribute.Key("net.protocol.version").String(arr[1]))
		}
		span.SetAttributes(attribute.Key("http.method").String(req.Method))
		span.SetAttributes(attribute.Key("http.request_content_length").Int64(req.ContentLength))
		span.SetAttributes(attribute.Key("http.url").String(clearUserPassRe.ReplaceAllString(req.URL.String(), "$1")))
		span.SetAttributes(attribute.Key("net.peer.name").String(req.URL.Hostname()))
		span.SetAttributes(attribute.Key("net.peer.port").String(req.URL.Port()))

		// 记录查询参数
		if req.URL.RawQuery != "" {
			span.SetAttributes(attribute.Key("http.query_params").String(req.URL.RawQuery))
		}

		// 记录请求头
		if req.Header != nil {
			headerStr := sanitizeHeadersForSpan(req.Header)
			if len(headerStr) > maxHeaderLogSize {
				headerStr = headerStr[:maxHeaderLogSize] + "...[truncated]"
			}
			span.SetAttributes(attribute.Key("http.request_headers").String(headerStr))
		}

		if req.Body != nil && req.ContentLength > 0 {
			span.SetAttributes(attribute.Key("http.request_body").String(requestBodyPolicyForSpan(req.ContentLength)))
		}

		if req.Header == nil {
			req.Header = http.Header{}
		}
		otel.GetTextMapPropagator().Inject(trace.ContextWithSpan(ctx, span), propagation.HeaderCarrier(req.Header))
		req = req.WithContext(ctx)
		defer func() {
			if rsp != nil {
				span.SetAttributes(attribute.Key("http.status_code").Int(rsp.StatusCode))
				span.SetAttributes(attribute.Key("http.response_content_length").Int64(rsp.ContentLength))
			}
			// 400以上的错误记录到trace中
			e := err
			if e == nil {
				e = recordHTTPErrorBody(rsp)
			}
			oteltrace.EndSpan(ctx, e)
		}()
	}
	rsp, err = fn(req)
	return
}

func sanitizeHeadersForSpan(headers http.Header) string {
	sanitized := map[string][]string{}
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		values := headers.Values(key)
		if isSensitiveHeader(key) {
			sanitized[key] = []string{"[redacted]"}
			continue
		}
		sanitized[key] = values
	}
	headerBytes, _ := json.Marshal(sanitized)
	return string(headerBytes)
}

func isSensitiveHeader(key string) bool {
	normalized := strings.ToLower(key)
	return normalized == "authorization" ||
		normalized == "x-authorization" ||
		normalized == "cookie" ||
		normalized == "set-cookie" ||
		strings.Contains(normalized, "token") ||
		strings.Contains(normalized, "api-key") ||
		strings.Contains(normalized, "apikey") ||
		strings.Contains(normalized, "secret")
}

func requestBodyPolicyForSpan(contentLength int64) string {
	return jsonCompact(map[string]interface{}{
		"redacted":       true,
		"content_length": contentLength,
		"reason":         "body omitted by BKN Trace phase-one sensitive data policy",
	})
}

func jsonCompact(value interface{}) string {
	bytes, err := json.Marshal(value)
	if err != nil {
		return `{"redacted":true}`
	}
	return string(bytes)
}

func recordHTTPErrorBody(rsp *http.Response) (err error) {
	// 只记录 400以上错误
	if rsp == nil || rsp.Body == nil {
		return nil
	}
	if rsp.StatusCode < http.StatusBadRequest {
		return
	}
	body, err := io.ReadAll(rsp.Body)
	if err != nil {
		return
	}
	rsp.Body = io.NopCloser(bytes.NewBuffer(body)) // 将body重新赋值给rsp.Body，后续可以读取
	limitBody := body
	err = errors.New(string(limitBody))
	return
}

// BuildUpOperateName 获取TraceOperate名称
func BuildUpOperateName(ops ...string) string {
	return strings.Join(ops, ".")
}
