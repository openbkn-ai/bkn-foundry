// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package rest

import (
	"net/http"

	"github.com/bytedance/sonic"
	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-comm-go/logger"
)

// golangci-lint 要求独立定义key的类型

const (
	ContentTypeKey  = "Content-Type"
	ContentTypeJson = "application/json"
)

// ReplyOK 响应成功
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
		}
	}

	c.Writer.Header().Set(ContentTypeKey, ContentTypeJson)
	c.String(statusCode, bodyStr)
}

func ReplyOkWithHeaders(c *gin.Context, statusCode int, body interface{}, headers map[string]string) {
	addHeaders(c, headers)
	ReplyOK(c, statusCode, body)
}

// ReplyError 响应错误
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
