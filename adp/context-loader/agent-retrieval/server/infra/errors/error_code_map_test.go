// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package errors

import (
	"context"
	"net/http"
	"testing"
)

// TestDistinguishableErrorCodes 盯住那些「模型据此决定要不要重试」的状态码。
//
// DefaultHTTPError 用 errCodeMap 反查 code 字符串，表里没有的码一律退回
// InternalServerError。MCP 工具层回给模型的是 HTTPError 的 JSON，模型读的是 code
// 而不是 HTTP status——413（文件太大）和 502（上游挂了）若都被说成内部错误，
// 模型就会去重试一个永远不会变小的文件。
func TestDistinguishableErrorCodes(t *testing.T) {
	statuses := []int{
		http.StatusRequestEntityTooLarge,
		http.StatusBadGateway,
		http.StatusBadRequest,
		http.StatusForbidden,
		http.StatusNotFound,
	}
	for _, status := range statuses {
		err := DefaultHTTPError(context.Background(), status, "detail")
		if err.HTTPCode != status {
			t.Fatalf("status %d: HTTPCode = %d", status, err.HTTPCode)
		}
		if err.Code == "" || err.Code == "agentRetrieval.InternalServerError" {
			t.Fatalf("status %d: code = %q，落回了 InternalServerError，调用方无从区分", status, err.Code)
		}
	}
}
