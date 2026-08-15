// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package object_type

import (
	"context"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	oerrors "ontology-query/errors"
)

func Test_downstreamErrorCode(t *testing.T) {
	// 状态码已经透传对了，错误码也要跟着走：403 贴成「参数错误」会被前端读成用户
	// 自己没权限，429 贴成「参数错误」会让调用方去改查询而不是重试。
	cases := map[int]string{
		http.StatusBadRequest:   oerrors.OntologyQuery_ObjectType_InvalidParameter,
		http.StatusUnauthorized: rest.PublicError_Unauthorized,
		http.StatusForbidden:    rest.PublicError_Forbidden,
		http.StatusNotFound:     rest.PublicError_NotFound,
		http.StatusConflict:     rest.PublicError_Conflict,
		// 没有语义对应的公共码时退回本服务的参数错误码，而不是 Public.BadRequest
		// ——后者的 en-US 文案是 "Internal Server Error"。
		http.StatusTooManyRequests:       oerrors.OntologyQuery_ObjectType_InvalidParameter,
		http.StatusRequestEntityTooLarge: oerrors.OntologyQuery_ObjectType_InvalidParameter,
	}
	for status, want := range cases {
		if got := downstreamErrorCode(status); got != want {
			t.Fatalf("status %d: got %q, want %q", status, got, want)
		}
	}
}

// rest.NewHTTPError 对未注册的错误码是 logger.Fatalf——只比对映射表的用例挡不住
// 「往 switch 里加了一个没进 allErrs 的码」，那种错误要到线上第一次命中才暴露，
// 而且直接打掉进程。这里把每个码真的构造一遍，并检查两种语言的文案都不为空。
func Test_downstreamErrorCodeIsRegisteredInEveryLanguage(t *testing.T) {
	statuses := []int{
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusConflict,
		http.StatusMethodNotAllowed,
		http.StatusRequestTimeout,
		http.StatusGone,
		http.StatusRequestEntityTooLarge,
		http.StatusUnsupportedMediaType,
		http.StatusUnprocessableEntity,
		http.StatusTooManyRequests,
	}
	for lang := range rest.Languages {
		for _, status := range statuses {
			ctx := context.WithValue(context.Background(), rest.LanguageKey, lang)
			httpErr := rest.NewHTTPError(ctx, status, downstreamErrorCode(status))
			if httpErr == nil {
				t.Fatalf("status %d lang %v: error code is not registered", status, lang)
			}
			if httpErr.BaseError.Description == "" {
				t.Fatalf("status %d lang %v: description is empty", status, lang)
			}
			if httpErr.HTTPCode != status {
				t.Fatalf("status %d lang %v: status code was rewritten to %d", status, lang, httpErr.HTTPCode)
			}
		}
	}
}
