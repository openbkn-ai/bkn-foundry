// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"
)

func Test_VegaDownstreamError(t *testing.T) {
	body := `{"error_code":"VegaBackend.Query.InvalidParameter","description":"查询参数错误",` +
		`"error_details":"operation match is not supported by the sql query channel; full-text operations need a local index"}`

	t.Run("4xx 认定为请求侧问题并保留最有信息量的一句", func(t *testing.T) {
		err := NewVegaDownstreamError(http.StatusBadRequest, body)
		if !err.IsClientError() {
			t.Fatal("400 from vega-backend is the caller's problem, not a dependency outage")
		}
		if err.ErrorCode != "VegaBackend.Query.InvalidParameter" {
			t.Fatalf("downstream error code lost: %q", err.ErrorCode)
		}
		// error_details is the message that can guide the caller's next step, so it takes precedence over a generic description.
		if got := err.Message(); got != "operation match is not supported by the sql query channel; full-text operations need a local index" {
			t.Fatalf("unexpected message: %q", got)
		}
	})

	t.Run("5xx 仍然是依赖故障", func(t *testing.T) {
		err := NewVegaDownstreamError(http.StatusInternalServerError, body)
		if err.IsClientError() {
			t.Fatal("500 must keep being treated as a dependency failure")
		}
	})

	t.Run("报文格式不认识时退回原始内容", func(t *testing.T) {
		err := NewVegaDownstreamError(http.StatusBadRequest, "boom")
		if got := err.Message(); got != "boom" {
			t.Fatalf("unparsable payload must survive, got %q", got)
		}
	})

	t.Run("解析不出结构的长报文被截断", func(t *testing.T) {
		// 4xx does not necessarily come from Vega: the gateway returns a full HTML page for 413/502, and falling back to the full body would make terminal
		// callers receive a blob of HTML in error_details as the error reason.
		html := "<html><body>" + strings.Repeat("x", 4096) + "</body></html>"
		msg := NewVegaDownstreamError(http.StatusRequestEntityTooLarge, html).Message()
		if len(msg) > maxRawMessageLen+len("...(truncated)") {
			t.Fatalf("raw payload must be truncated, got %d bytes", len(msg))
		}
		if !strings.HasSuffix(msg, "...(truncated)") {
			t.Fatalf("truncation must be visible, got %q", msg[len(msg)-32:])
		}
	})

	t.Run("截断不会切开多字节字符", func(t *testing.T) {
		// When Vega Chinese error bodies or localized gateway error pages exceed the limit, byte-based truncation can cut in the middle of a UTF-8
		// sequence and leave a partial character in error_details for callers.
		raw := strings.Repeat("知识网络查询失败", 200)
		msg := NewVegaDownstreamError(http.StatusBadGateway, raw).Message()
		if !utf8.ValidString(msg) {
			t.Fatalf("truncated message must stay valid UTF-8: %q", msg)
		}
		if !strings.HasSuffix(msg, "...(truncated)") {
			t.Fatal("truncation must stay visible")
		}
	})

	t.Run("包装后仍可识别", func(t *testing.T) {
		wrapped := fmt.Errorf("query failed: %w", NewVegaDownstreamError(http.StatusNotFound, body))
		downstream, ok := AsVegaDownstreamError(wrapped)
		if !ok {
			t.Fatal("errors.As must find the downstream error through wrapping")
		}
		// Keep 404 as 404: unknown resources and invalid parameters are different caller errors, and collapsing to 400 loses that distinction.
		if downstream.StatusCode != http.StatusNotFound {
			t.Fatalf("status code must survive, got %d", downstream.StatusCode)
		}
	})
}
