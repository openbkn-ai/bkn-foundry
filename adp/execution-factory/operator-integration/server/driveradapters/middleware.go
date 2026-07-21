// Package driveradapters 定义驱动适配器
// @file middleware.go
// @description: 中间件适配器
package driveradapters

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	jsoniter "github.com/json-iterator/go"
	"github.com/openbkn-ai/bkn-comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/attribute"

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/drivenadapters"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/common"
	oerrors "github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/rest"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
)

type apiLogModel struct {
	URI          string      `json:"uri"`
	Method       string      `json:"method"`
	RemoteAddr   string      `json:"remoteAddr"`
	RequestBody  interface{} `json:"requestBodySummary"`
	ResponseCode int         `json:"responseCode"`
	ResponseBody interface{} `json:"ResponseBody"`
	Latency      float64     `json:"latency"` // 单位(ms)
}

// middlewareIntrospectVerify 令牌内省中间件
// 若未开启认证，则从header中获取accountID和accountType，生成匿名tokenInfo
// 若开启认证，则从header中获取token，调用hydra.Introspect验证token，若验证失败则返回错误
//
// 凭据二选一：以 AppKey 前缀（bak_）开头的交给 bkn-safe 校验（用户自助签发的 API Key），
// 其余 bearer token 走 hydra 内省。两条路产出同一个 TokenInfo，下游认证上下文一致。
// appKeys 为 nil 时（AUTH_ENABLED=false 或 BKN_SAFE_URL 未配置）全部走 hydra。
func middlewareIntrospectVerify(hydra interfaces.Hydra, appKeys interfaces.AppKeyVerifier) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		var tokenInfo *interfaces.TokenInfo
		var err error
		if token := drivenadapters.GetToken(c); appKeys != nil && strings.HasPrefix(token, interfaces.AppKeyPrefix) {
			tokenInfo, err = appKeys.Verify(ctx, token)
		} else {
			tokenInfo, err = hydra.Introspect(c)
		}
		if err != nil {
			rest.ReplyError(c, err)
			c.Abort()
			return
		}
		// 设置认证上下文到context
		authContext := &interfaces.AccountAuthContext{
			AccountID:   tokenInfo.VisitorID,
			AccountType: tokenInfo.VisitorTyp.ToAccessorType(),
			TokenInfo:   tokenInfo,
		}
		ctx = common.SetAccountAuthContextToCtx(ctx, authContext)
		ctx = common.SetLanguageToCtx(ctx, common.GetLanguageInfo(c)) // 设置language信息到context
		ctx = common.SetPublicAPIToCtx(ctx, true)                     // 设置是否为公共API到context
		c.Request = c.Request.WithContext(ctx)
		c.Request.Header.Set(string(interfaces.HeaderUserID), tokenInfo.VisitorID)
		c.Request.Header.Set(string(interfaces.IsPublic), "true")
		c.Next()
	}
}

// 内部接口Header认证账户信息处理中间件
func middlewareHeaderAuthContext(hydra interfaces.Hydra) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		tokenInfo, err := hydra.GenerateVisitor(c)
		if err != nil {
			rest.ReplyError(c, err)
			c.Abort()
			return
		}
		// 设置认证上下文到context
		authContext := &interfaces.AccountAuthContext{
			AccountID:   tokenInfo.VisitorID,
			AccountType: tokenInfo.VisitorTyp.ToAccessorType(),
			TokenInfo:   tokenInfo,
		}
		ctx = common.SetAccountAuthContextToCtx(ctx, authContext)
		c.Request = c.Request.WithContext(ctx)
		c.Request.Header.Set(string(interfaces.HeaderUserID), tokenInfo.VisitorID)
		c.Next()
	}
}

func middlewareRequestLog(logger interfaces.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		now := time.Now()
		req, err := io.ReadAll(c.Request.Body)
		if err != nil {
			err = oerrors.DefaultHTTPError(c.Request.Context(), http.StatusInternalServerError, err.Error())
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
			RequestBody:  safeRequestBodySummary(c.Request.Header.Get("Content-Type"), req),
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

func middlewareTraceContext(c *gin.Context) {
	ctx := common.SetTraceContextToCtx(c.Request.Context(), common.TraceContextFromHeaders(c.GetHeader))
	traceContext, _ := common.GetTraceContextFromCtx(ctx)
	c.Request.Header.Set(common.HeaderBKNRequestID, traceContext.RequestID)
	c.Request.Header.Set(common.HeaderLegacyRequestID, traceContext.RequestID)
	c.Request = c.Request.WithContext(ctx)
	c.Next()
}

func safeRequestBodySummary(contentType string, body []byte) map[string]interface{} {
	summary := map[string]interface{}{
		"content_type": contentType,
		"length":       len(body),
	}
	if len(body) == 0 {
		return summary
	}
	sum := sha256.Sum256(body)
	summary["hash"] = fmt.Sprintf("sha256:%x", sum[:])
	return summary
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

// middlewareBusinessDomain 处理x-business-domain逻辑
func middlewareBusinessDomain(isPublic, isBuiltin bool, businessDomainService interfaces.IBusinessDomainService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		businessDomain := businessDomainService.GetBusinessDomainFromHeader(c)
		// 初始化默认值
		// 1. 外部接口：如果不传递，默认bd_public
		if isPublic {
			if businessDomain == "" {
				businessDomain = interfaces.DefaultBusinessDomain
				c.Request.Header.Set(string(interfaces.HeaderXBusinessDomain), businessDomain)
			}
		} else {
			// 2. 内部接口中的内置算子、工具、MCP：默认bd_public
			if isBuiltin {
				if businessDomain == "" {
					businessDomain = interfaces.DefaultBusinessDomain
					c.Request.Header.Set(string(interfaces.HeaderXBusinessDomain), businessDomain)
				}
			}
		}
		// 设置到context中供后续使用
		ctx = common.SetBusinessDomainToCtx(ctx, businessDomain)
		c.Request = c.Request.WithContext(ctx)
		// 3. 校验业务域是否存在
		err := businessDomainService.ValidateBusinessDomain(ctx)
		if err != nil {
			rest.ReplyError(c, err)
			c.Abort()
			return
		}
		c.Next()
	}
}

// middlewareProxyRequest 识别代理请求并设置上下文信息
func middlewareProxyRequest() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 识别请求类型（同步/流式）及流类型
		isStreaming := isStreamingRequest(c)
		if !isStreaming {
			c.Next()
			return
		}
		executionMode := interfaces.ExecutionModeStream
		streamingMode := detectStreamingMode(c)
		// 然后设置上下文和请求头
		ctx := c.Request.Context()
		ctx = common.SetResponseWriterToCtx(ctx, c.Writer)
		ctx = common.SetExecutionModeToCtx(ctx, executionMode)
		ctx = common.SetStreamingModeToCtx(ctx, streamingMode)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// isStreamingRequest 判断是否为流式请求
func isStreamingRequest(c *gin.Context) bool {
	if c.Query("stream") == "true" {
		return true
	}
	accept := c.GetHeader("Accept")
	switch accept {
	case "text/event-stream":
		return true
	case "application/stream+json":
		return true
	default:
		return false
	}
}

// detectStreamingMode 检测流式模式
func detectStreamingMode(c *gin.Context) interfaces.StreamingMode {
	streamMode := c.Query("mode")
	switch interfaces.StreamingMode(streamMode) {
	case interfaces.StreamingModeSSE:
		return interfaces.StreamingModeSSE
	case interfaces.StreamingModeHTTP:
		return interfaces.StreamingModeHTTP
	}
	accept := c.GetHeader("Accept")
	switch accept {
	case "text/event-stream":
		return interfaces.StreamingModeSSE
	case "application/stream+json":
		return interfaces.StreamingModeHTTP
	default:
		return interfaces.StreamingModeHTTP
	}
}
