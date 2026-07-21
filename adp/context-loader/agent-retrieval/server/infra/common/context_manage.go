// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package common

import (
	"context"
	"crypto/rand"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/interfaces"
	"go.opentelemetry.io/otel/trace"
)

const (
	HeaderTraceparent     = "traceparent"
	HeaderBKNRequestID    = "bkn-request-id"
	HeaderLegacyRequestID = "x-request-id"
	HeaderBaggage         = "baggage"
)

type traceContextKey string

const keyTraceContext traceContextKey = "bkn_trace_context"

var bknRequestIDRe = regexp.MustCompile(`^req_[A-Za-z0-9_-]{8,128}$`)

// TraceContext carries the OpenBKN phase-one correlation context.
type TraceContext struct {
	RequestID string
	Baggage   map[string]string
}

// GetLanguageFromCtx 从context中获取语言设置
func GetLanguageFromCtx(ctx context.Context) Language {
	return GetLanguageByCtx(ctx)
}

// SetLanguageToCtx 设置语言到context
func SetLanguageToCtx(ctx context.Context, languageInfo Language) context.Context {
	return SetLanguageByCtx(ctx, languageInfo)
}

func SetPublicAPIToCtx(ctx context.Context, isPublic bool) context.Context {
	return context.WithValue(ctx, interfaces.IsPublic, isPublic)
}

// IsPublicAPIFromCtx 判断是否为公开API
func IsPublicAPIFromCtx(ctx context.Context) bool {
	if isPublic, ok := ctx.Value(interfaces.IsPublic).(bool); ok {
		return isPublic
	}
	return false
}

// SetAccountAuthContextToCtx 设置账户认证上下文到context
func SetAccountAuthContextToCtx(ctx context.Context, authContext *interfaces.AccountAuthContext) context.Context {
	return context.WithValue(ctx, interfaces.KeyAccountAuthContext, authContext)
}

func GetAccountAuthContextFromCtx(ctx context.Context) (*interfaces.AccountAuthContext, bool) {
	authContext, ok := ctx.Value(interfaces.KeyAccountAuthContext).(*interfaces.AccountAuthContext)
	return authContext, ok
}

// GetTokenInfoFromCtx 从context中获取token信息
func GetTokenInfoFromCtx(ctx context.Context) (*interfaces.TokenInfo, bool) {
	authContext, ok := GetAccountAuthContextFromCtx(ctx)
	if !ok {
		return nil, false
	}
	if authContext.TokenInfo == nil {
		return nil, false
	}
	return authContext.TokenInfo, true
}

// SetResponseFormatToCtx 设置响应格式到 context（用于 HTTP 序列化出口）
func SetResponseFormatToCtx(ctx context.Context, format interface{}) context.Context {
	return context.WithValue(ctx, interfaces.KeyResponseFormat, format)
}

// GetResponseFormatFromCtx 从 context 获取响应格式，不存在时返回 nil（调用方按默认 json 处理）
func GetResponseFormatFromCtx(ctx context.Context) (interface{}, bool) {
	v := ctx.Value(interfaces.KeyResponseFormat)
	return v, v != nil
}

func SetTraceContextToCtx(ctx context.Context, traceContext TraceContext) context.Context {
	if !IsValidBKNRequestID(traceContext.RequestID) {
		traceContext.RequestID = NewBKNRequestID()
	}
	traceContext.Baggage = sanitizeBaggage(traceContext.Baggage)
	return context.WithValue(ctx, keyTraceContext, traceContext)
}

func GetTraceContextFromCtx(ctx context.Context) (TraceContext, bool) {
	traceContext, ok := ctx.Value(keyTraceContext).(TraceContext)
	return traceContext, ok
}

func TraceContextFromHeaders(getHeader func(string) string) TraceContext {
	requestID := strings.TrimSpace(getHeader(HeaderBKNRequestID))
	if requestID == "" {
		requestID = strings.TrimSpace(getHeader(HeaderLegacyRequestID))
	}
	return TraceContext{
		RequestID: requestID,
		Baggage:   parseBaggage(getHeader(HeaderBaggage)),
	}
}

func IsValidBKNRequestID(requestID string) bool {
	return bknRequestIDRe.MatchString(requestID)
}

func NewBKNRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "req_fallback"
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("req_%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// GetHeaderFromCtx 请求外部接口时，从context中获取Header参数传递
func GetHeaderFromCtx(ctx context.Context) (header map[string]string) {
	header = map[string]string{}
	authContext, ok := GetAccountAuthContextFromCtx(ctx)
	if ok {
		header[string(interfaces.HeaderXAccountID)] = authContext.AccountID
		header[string(interfaces.HeaderXAccountType)] = string(authContext.AccountType)
	}
	traceContext, ok := GetTraceContextFromCtx(ctx)
	if ok {
		header[HeaderBKNRequestID] = traceContext.RequestID
		header[HeaderLegacyRequestID] = traceContext.RequestID
		if baggage := formatBaggage(traceContext.Baggage); baggage != "" {
			header[HeaderBaggage] = baggage
		}
	}
	if traceparent := traceparentFromCtx(ctx); traceparent != "" {
		header[HeaderTraceparent] = traceparent
	}
	return
}

func sanitizeBaggage(baggage map[string]string) map[string]string {
	if len(baggage) == 0 {
		return nil
	}
	cleaned := map[string]string{}
	for key, value := range baggage {
		switch key {
		case "bkn.account.type", "bkn.runtime.env":
			cleaned[key] = value
		}
	}
	if len(cleaned) == 0 {
		return nil
	}
	return cleaned
}

func parseBaggage(header string) map[string]string {
	if strings.TrimSpace(header) == "" {
		return nil
	}
	baggage := map[string]string{}
	for _, item := range strings.Split(header, ",") {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" {
			continue
		}
		baggage[key] = value
	}
	if len(baggage) == 0 {
		return nil
	}
	return baggage
}

func formatBaggage(baggage map[string]string) string {
	if len(baggage) == 0 {
		return ""
	}
	keys := make([]string, 0, len(baggage))
	for key := range baggage {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+strings.TrimSpace(baggage[key]))
	}
	return strings.Join(parts, ",")
}

func traceparentFromCtx(ctx context.Context) string {
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return ""
	}
	flags := "00"
	if spanContext.TraceFlags().IsSampled() {
		flags = "01"
	}
	return fmt.Sprintf("00-%s-%s-%s", spanContext.TraceID().String(), spanContext.SpanID().String(), flags)
}
