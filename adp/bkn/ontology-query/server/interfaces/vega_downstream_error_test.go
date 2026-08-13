// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"fmt"
	"net/http"
	"testing"
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
		// error_details 才是能指导调用方下一步的那句，优先于笼统的 description。
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

	t.Run("包装后仍可识别", func(t *testing.T) {
		wrapped := fmt.Errorf("query failed: %w", NewVegaDownstreamError(http.StatusNotFound, body))
		downstream, ok := AsVegaDownstreamError(wrapped)
		if !ok {
			t.Fatal("errors.As must find the downstream error through wrapping")
		}
		// 404 保持 404：未知资源与参数非法是两种不同的调用方错误，压成 400 会丢掉区别。
		if downstream.StatusCode != http.StatusNotFound {
			t.Fatalf("status code must survive, got %d", downstream.StatusCode)
		}
	})
}
