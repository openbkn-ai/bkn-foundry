// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package object_type

import (
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	oerrors "ontology-query/errors"
)

func Test_downstreamErrorCode(t *testing.T) {
	// 状态码已经透传对了，错误码也要跟着走：403 贴成「参数错误」会被前端读成用户
	// 自己没权限，429 贴成「参数错误」会让调用方去改查询而不是重试。
	cases := map[int]string{
		http.StatusBadRequest:      oerrors.OntologyQuery_ObjectType_InvalidParameter,
		http.StatusUnauthorized:    rest.PublicError_Unauthorized,
		http.StatusForbidden:       rest.PublicError_Forbidden,
		http.StatusNotFound:        rest.PublicError_NotFound,
		http.StatusConflict:        rest.PublicError_Conflict,
		http.StatusTooManyRequests: rest.PublicError_BadRequest,
	}
	for status, want := range cases {
		if got := downstreamErrorCode(status); got != want {
			t.Fatalf("status %d: got %q, want %q", status, got, want)
		}
	}
}
