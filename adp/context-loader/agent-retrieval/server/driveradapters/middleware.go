// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package driveradapters 定义驱动适配器
// @file middleware.go
// @description: 中间件适配器
package driveradapters

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	jsoniter "github.com/json-iterator/go"
	"github.com/openbkn-ai/bkn-comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/attribute"

	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/infra/common"
	aerrors "github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/interfaces"
)

type apiLogModel struct {
	URI          string      `json:"uri"`
	Method       string      `json:"method"`
	RemoteAddr   string      `json:"remoteAddr"`
	RequestBody  interface{} `json:"requestBody"`
	ResponseCode int         `json:"responseCode"`
	ResponseBody interface{} `json:"ResponseBody"`
	Latency      float64     `json:"latency"` // 单位(ms)
}

func getToken(c *gin.Context) (token string) {
	tokenID := c.GetHeader("Authorization")
	if tokenID == "" {
		tokenID = c.GetHeader("X-Authorization")
	}
	if tokenID == "" {
		token, _ = c.GetQuery("token")
	} else {
		token = strings.TrimPrefix(tokenID, "Bearer ")
	}
	return token
}

// middlewareIntrospect 令牌内省中间件。
// 凭据二选一:以 AppKey 前缀(bak_)开头的交给 bkn-safe 校验(用户自助签发的 API Key),
// 其余 bearer token 走 hydra 内省。两条路产出同一个 TokenInfo,下游认证上下文一致。
func middlewareIntrospectVerify(hydra interfaces.Hydra, appKeys interfaces.AppKeyVerifier) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		// 设置language信息到context
		ctx = common.SetLanguageToCtx(ctx, common.GetLanguageInfo(c))

		token := getToken(c)
		var tokenInfo *interfaces.TokenInfo
		var err error
		if appKeys != nil && strings.HasPrefix(token, interfaces.AppKeyPrefix) {
			tokenInfo, err = appKeys.Verify(ctx, token)
		} else {
			tokenInfo, err = hydra.Introspect(ctx, token)
		}
		if err != nil {
			rest.ReplyError(c, err)
			c.Abort()
			return
		}
		if tokenInfo.LoginIP == "" {
			// 若返回IP为空则使用clientIP
			tokenInfo.LoginIP = c.ClientIP()
		}
		tokenInfo.MAC = c.GetHeader("X-Request-MAC")
		tokenInfo.UserAgent = c.GetHeader("User-Agent")

		ctx = common.SetPublicAPIToCtx(ctx, true)
		ctx = common.SetTraceContextToCtx(ctx, common.TraceContextFromHeaders(c.GetHeader))
		// 设置认证上下文到context
		authContext := &interfaces.AccountAuthContext{
			AccountID:   tokenInfo.VisitorID,
			AccountType: tokenInfo.VisitorTyp.ToAccessorType(),
			TokenInfo:   tokenInfo,
		}
		ctx = common.SetAccountAuthContextToCtx(ctx, authContext)
		c.Request = c.Request.WithContext(ctx)
		c.Request.Header.Set(string(interfaces.IsPublic), "true")
		c.Next()
	}
}

// 内部接口Header认证账户信息处理中间件
func middlewareHeaderAuthContext() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		ctx = common.SetTraceContextToCtx(ctx, common.TraceContextFromHeaders(c.GetHeader))
		// 获取Header中xAccountType账户类型
		xAccountType := c.GetHeader(string(interfaces.HeaderXAccountType))

		// 兼容user_id传参，当user_id为空时，使用xAccountID
		xAccountID := c.GetHeader(string(interfaces.HeaderUserID))
		if xAccountID == "" {
			xAccountID = c.GetHeader(string(interfaces.HeaderXAccountID))
		}
		// 将user_id设置到Header中,TODO:是否需要检查必填？
		c.Request.Header.Set(string(interfaces.HeaderUserID), xAccountID)
		// 设置认证上下文到context
		authContext := &interfaces.AccountAuthContext{
			AccountID:   xAccountID,
			AccountType: interfaces.AccessorType(xAccountType),
			TokenInfo: &interfaces.TokenInfo{
				VisitorID:  xAccountID,
				VisitorTyp: interfaces.AccessorType(xAccountType).ToVisitorType(),
			},
		}
		ctx = common.SetAccountAuthContextToCtx(ctx, authContext)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

func middlewareRequestLog(logger interfaces.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		now := time.Now()
		req, err := io.ReadAll(c.Request.Body)
		if err != nil {
			err = aerrors.DefaultHTTPError(c.Request.Context(), http.StatusInternalServerError, err.Error())
			rest.ReplyError(c, err)
			c.Abort()
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewBuffer(req))
		c.Next()
		logPayload, _ := jsoniter.MarshalToString(apiLogModel{
			URI:          c.Request.RequestURI,
			Method:       c.Request.Method,
			RemoteAddr:   c.Request.RemoteAddr,
			RequestBody:  redactSensitiveFields(byteToInterface(req)),
			ResponseCode: c.Writer.Status(),
			Latency:      float64(time.Since(now).Nanoseconds()) / 1e6, //nolint:mnd
		})
		logger.WithContext(c.Request.Context()).Infof("HTTP API Log : %s", logPayload)
	}
}

func middlewareTrace(c *gin.Context) {
	ctx := oteltrace.ExtractTraceHeader(c.Request.Context(), c.Request.Header)
	c.Request = c.Request.WithContext(ctx)

	ctx, span := oteltrace.StartServerSpan(c)
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))
	scheme := interfaces.HTTPS
	if c.Request.TLS == nil {
		scheme = interfaces.HTTP
	}
	span.SetAttributes(attribute.Key("http.scheme").String(scheme))
	c.Request = c.Request.WithContext(ctx)
	defer func() {
		if c.Writer.Status() >= http.StatusBadRequest {
			statusText := http.StatusText(c.Writer.Status())
			oteltrace.AddHttpAttrs4Error(span, c.Writer.Status(), "HTTP_ERROR", statusText)
			oteltrace.EndSpan(ctx, errors.New(statusText))
			return
		}
		oteltrace.AddHttpAttrs4Ok(span, c.Writer.Status())
		oteltrace.EndSpan(ctx, c.Request.Context().Err())
	}()
	c.Next()
}

func byteToInterface(byt []byte) interface{} {
	m := map[string]interface{}{}
	err := jsoniter.Unmarshal(byt, &m)
	if err == nil {
		return m
	}
	s := []interface{}{}
	err = jsoniter.Unmarshal(byt, &s)
	if err == nil {
		return s
	}

	m["string"] = string(byt)
	return m
}

// sensitiveBodyKeys 是请求体中需要在日志里脱敏的字段名。dynamic_params 是任意
// 工具输入，可能含令牌/密码等敏感值（见 PR #379 review P1）；对其值整体脱敏，
// 仅保留字段名以维持可观测性。
var sensitiveBodyKeys = map[string]struct{}{
	"dynamic_params": {},
}

// redactSensitiveFields 递归遍历已解析的请求体（map / slice），将 sensitiveBodyKeys
// 命中的字段值替换为脱敏标记，其余结构原样保留。覆盖 REST 顶层 dynamic_params 与
// MCP JSON-RPC 嵌套的 params.arguments.dynamic_params 两种形态。
func redactSensitiveFields(v interface{}) interface{} {
	switch val := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(val))
		for k, vv := range val {
			if _, ok := sensitiveBodyKeys[k]; ok {
				out[k] = "[REDACTED]"
				continue
			}
			out[k] = redactSensitiveFields(vv)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(val))
		for i, vv := range val {
			out[i] = redactSensitiveFields(vv)
		}
		return out
	default:
		return v
	}
}

// middlewareResponseFormat 解析 Query 参数 response_format（默认 json），非法值返回 400，并写入 context
func middlewareResponseFormat() gin.HandlerFunc {
	return func(c *gin.Context) {
		formatStr := c.Query("response_format")
		if formatStr == "" {
			formatStr = "json"
		}
		format, err := rest.ParseResponseFormat(formatStr)
		if err != nil {
			rest.ReplyError(c, aerrors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error()))
			c.Abort()
			return
		}
		ctx := common.SetResponseFormatToCtx(c.Request.Context(), format)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}
