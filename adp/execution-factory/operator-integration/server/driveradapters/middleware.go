// Package driveradapters defines driver adapters.
// @file middleware.go
// @description: middleware adapter.
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
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/attribute"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	oerrors "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

type apiLogModel struct {
	URI          string      `json:"uri"`
	Method       string      `json:"method"`
	RemoteAddr   string      `json:"remoteAddr"`
	RequestBody  interface{} `json:"requestBodySummary"`
	ResponseCode int         `json:"responseCode"`
	ResponseBody interface{} `json:"ResponseBody"`
	Latency      float64     `json:"latency"` // Unit(ms)
}

// middlewareIntrospectVerify token introspection middleware.
// If authentication is not enabled, obtain accountID and accountType from the header and generate anonymous tokenInfo.
// If authentication is turned on, get the token from the header and call hydra.Introspect to verify the token. If the verification fails, an error will be returned.
//
// Choose one of two credentials: the one starting with the AppKey prefix (bak_) is submitted to bkn-safe for verification (API Key issued by the user),
// The rest of the bearer token goes hydra introspection. The two paths produce the same TokenInfo, and the downstream authentication context is consistent.
// When appKeys is nil (AUTH_ENABLED=false), all requests use Hydra.
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
		// Set authentication context to context.
		authContext := &interfaces.AccountAuthContext{
			AccountID:   tokenInfo.VisitorID,
			AccountType: tokenInfo.VisitorTyp.ToAccessorType(),
			TokenInfo:   tokenInfo,
		}
		ctx = common.SetAccountAuthContextToCtx(ctx, authContext)
		ctx = common.SetLanguageToCtx(ctx, common.GetLanguageInfo(c)) // Set language information to context.
		ctx = common.SetPublicAPIToCtx(ctx, true)                     // Set whether it is a public API to context.
		c.Request = c.Request.WithContext(ctx)
		c.Request.Header.Set(string(interfaces.HeaderUserID), tokenInfo.VisitorID)
		c.Request.Header.Set(string(interfaces.IsPublic), "true")
		c.Next()
	}
}

// Internal interface Header authentication account information processing middleware.
func middlewareHeaderAuthContext(hydra interfaces.Hydra) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		tokenInfo, err := hydra.GenerateVisitor(c)
		if err != nil {
			rest.ReplyError(c, err)
			c.Abort()
			return
		}
		// Set authentication context to context.
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

// middlewareProxyRequest identifies proxy requests and sets context information.
func middlewareProxyRequest() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Identify request type (synchronous/streaming) and stream type.
		isStreaming := isStreamingRequest(c)
		if !isStreaming {
			c.Next()
			return
		}
		executionMode := interfaces.ExecutionModeStream
		streamingMode := detectStreamingMode(c)
		// Then set the context and request headers.
		ctx := c.Request.Context()
		ctx = common.SetResponseWriterToCtx(ctx, c.Writer)
		ctx = common.SetExecutionModeToCtx(ctx, executionMode)
		ctx = common.SetStreamingModeToCtx(ctx, streamingMode)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// isStreamingRequest determines whether it is a streaming request.
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

// detectStreamingMode detects streaming mode.
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
