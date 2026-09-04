// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package driveradapters defines driver adapters.
// @file middleware.go
// @description: middleware adapter.
package driveradapters

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	jsoniter "github.com/json-iterator/go"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/attribute"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	aerrors "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

type apiLogModel struct {
	URI          string      `json:"uri"`
	Method       string      `json:"method"`
	RemoteAddr   string      `json:"remoteAddr"`
	RequestBody  interface{} `json:"requestBody"`
	ResponseCode int         `json:"responseCode"`
	ResponseBody interface{} `json:"ResponseBody"`
	Latency      float64     `json:"latency"` // Unit(ms)
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

// middlewareIntrospect token introspection middleware.
// Choose one of the two credentials: the one starting with the AppKey prefix (bak_) is submitted to bkn-safe for verification (API Key issued by the user),
// The rest of the bearer token goes hydra introspection. The two paths produce the same TokenInfo, and the downstream authentication context is consistent.
func middlewareIntrospectVerify(hydra interfaces.Hydra, appKeys interfaces.AppKeyVerifier) gin.HandlerFunc {
	strict := config.GetAuthEnabled()
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		// Set language information to context.
		ctx = common.SetLanguageToCtx(ctx, common.GetLanguageInfo(c))

		token := getToken(c)
		authMethod := "oauth"
		var tokenInfo *interfaces.TokenInfo
		var err error
		if appKeys != nil && strings.HasPrefix(token, interfaces.AppKeyPrefix) {
			authMethod = "api_key"
			tokenInfo, err = appKeys.Verify(ctx, token)
		} else {
			tokenInfo, err = hydra.Introspect(ctx, token)
		}
		if err != nil {
			rest.ReplyError(c, err)
			c.Abort()
			return
		}
		if tokenInfo == nil {
			rest.ReplyError(c, aerrors.DefaultHTTPError(ctx, http.StatusUnauthorized,
				"authenticated execution subject is missing"))
			c.Abort()
			return
		}
		accountType := tokenInfo.VisitorTyp.ToAccessorType()
		if strict && !validInternalExecutionSubject(tokenInfo.VisitorID, accountType) {
			rest.ReplyError(c, aerrors.DefaultHTTPError(ctx, http.StatusUnauthorized,
				"authenticated execution subject is missing or invalid"))
			c.Abort()
			return
		}
		if tokenInfo.LoginIP == "" {
			// If the returned IP is empty, use clientIP.
			tokenInfo.LoginIP = c.ClientIP()
		}
		tokenInfo.MAC = c.GetHeader("X-Request-MAC")
		tokenInfo.UserAgent = c.GetHeader("User-Agent")

		ctx = common.SetPublicAPIToCtx(ctx, true)
		// The original token remains in the context: PTC's run_code needs to open the execution factory as the caller himself.
		// public side, so that the execute permission determination there will still take effect (see SetRawTokenToCtx).
		ctx = common.SetRawTokenToCtx(ctx, token)
		ctx = common.SetTraceContextToCtx(ctx, common.TraceContextFromHeaders(c.GetHeader))
		// Set the authentication context on the context.
		authContext := &interfaces.AccountAuthContext{
			AccountID:   tokenInfo.VisitorID,
			AccountType: accountType,
			AuthMethod:  authMethod,
			TokenInfo:   tokenInfo,
		}
		ctx = common.SetAccountAuthContextToCtx(ctx, authContext)
		// Normalize caller-controlled aliases before handlers bind headers. The
		// context remains authoritative for all outbound propagation, and request
		// DTOs observe the same authenticated subject rather than spoofed values.
		c.Request.Header.Set(string(interfaces.HeaderXAccountID), authContext.AccountID)
		c.Request.Header.Set(string(interfaces.HeaderXAccountType), string(authContext.AccountType))
		c.Request.Header.Set(string(interfaces.HeaderUserID), authContext.AccountID)
		c.Request = c.Request.WithContext(ctx)
		c.Request.Header.Set(string(interfaces.IsPublic), "true")
		c.Next()
	}
}

// Internal interface Header authentication account information processing middleware.
func middlewareHeaderAuthContext() gin.HandlerFunc {
	strict := config.GetAuthEnabled()
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		ctx = common.SetTraceContextToCtx(ctx, common.TraceContextFromHeaders(c.GetHeader))
		// Get the xAccountType account type in the Header.
		rawAccountType := c.GetHeader(string(interfaces.HeaderXAccountType))
		rawUserID := c.GetHeader(string(interfaces.HeaderUserID))
		rawAccountID := c.GetHeader(string(interfaces.HeaderXAccountID))
		xAccountType, userID, xAccountID := rawAccountType, rawUserID, rawAccountID
		if strict {
			xAccountType = strings.TrimSpace(rawAccountType)
			userID = strings.TrimSpace(rawUserID)
			xAccountID = strings.TrimSpace(rawAccountID)
			if rawUserID != userID || rawAccountID != xAccountID || rawAccountType != xAccountType {
				rest.ReplyError(c, aerrors.DefaultHTTPError(ctx, http.StatusUnauthorized,
					"internal execution subject contains invalid whitespace"))
				c.Abort()
				return
			}
		}

		// Compatible with user_id parameter passing, when user_id is empty, xAccountID is used.
		if strict && userID != "" && xAccountID != "" && userID != xAccountID {
			rest.ReplyError(c, aerrors.DefaultHTTPError(ctx, http.StatusUnauthorized,
				"conflicting internal execution subject headers"))
			c.Abort()
			return
		}
		if userID != "" {
			xAccountID = userID
		}
		if strict && xAccountType == "realname" {
			xAccountType = string(interfaces.AccessorTypeUser)
		}
		if strict && !validInternalExecutionSubject(xAccountID, interfaces.AccessorType(xAccountType)) {
			rest.ReplyError(c, aerrors.DefaultHTTPError(ctx, http.StatusUnauthorized,
				"internal execution subject is missing or invalid"))
			c.Abort()
			return
		}
		// Set user_id to Header, TODO: Do you need to check required?.
		c.Request.Header.Set(string(interfaces.HeaderUserID), xAccountID)
		// Set the authentication context on the context.
		authContext := &interfaces.AccountAuthContext{
			AccountID:   xAccountID,
			AccountType: interfaces.AccessorType(xAccountType),
			AuthMethod:  "service_header",
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

func validInternalExecutionSubject(accountID string, accountType interfaces.AccessorType) bool {
	if accountID == "" || strings.TrimSpace(accountID) != accountID || strings.ContainsAny(accountID, " \t\r\n*") {
		return false
	}
	switch accountType {
	case interfaces.AccessorTypeUser, interfaces.AccessorTypeApp:
		return true
	default:
		return false
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
			RequestBody:  requestBodyForLog(c.Request.URL.Path, req),
			ResponseCode: c.Writer.Status(),
			Latency:      float64(time.Since(now).Nanoseconds()) / 1e6, //nolint:mnd
		})
		logger.WithContext(c.Request.Context()).Infof("HTTP API Log : %s", logPayload)
	}
}

func requestBodyForLog(path string, body []byte) interface{} {
	if strings.Contains(path, "/mcp/") || strings.HasSuffix(path, "/mcp") {
		return mcpRequestBodyForLog(body)
	}
	return redactSensitiveFields(byteToInterface(body))
}

func mcpRequestBodyForLog(body []byte) map[string]interface{} {
	summary := map[string]interface{}{
		"content_length": len(body),
		"redacted":       true,
		"reason":         "MCP arguments are governed evidence, not general log content",
	}
	parsed, ok := byteToInterface(body).(map[string]interface{})
	if !ok {
		return summary
	}
	if method, ok := parsed["method"].(string); ok {
		summary["jsonrpc_method"] = method
	}
	params, ok := parsed["params"].(map[string]interface{})
	if !ok {
		return summary
	}
	if toolName, ok := params["name"].(string); ok {
		summary["tool_name"] = toolName
	}
	arguments, ok := params["arguments"].(map[string]interface{})
	if !ok {
		return summary
	}
	argumentKeys := make([]string, 0, len(arguments))
	for key := range arguments {
		argumentKeys = append(argumentKeys, key)
	}
	sort.Strings(argumentKeys)
	summary["argument_keys"] = argumentKeys
	return summary
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

// sensitiveBodyKeys is the field name in the request body that needs to be desensitized in the log. dynamic_params is arbitrary.
// Tool input may contain sensitive values such as tokens/passwords (see PR #379 review P1); overall desensitization of its values,
// Only field names are preserved to maintain observability.
var sensitiveBodyKeys = map[string]struct{}{
	"dynamic_params": {},
}

// redactSensitiveFields recursively traverses the parsed request body (map/slice) and adds sensitiveBodyKeys.
// Hit field values are replaced with desensitization markers, and the rest of the structure is left intact. Override REST top-level dynamic_params with.
// There are two forms of MCP JSON-RPC nested params.arguments.dynamic_params.
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

// middlewareResponseFormat parses the Query parameter response_format (default json), returns 400 for illegal values, and writes it to context.
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
