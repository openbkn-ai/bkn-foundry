package common

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"

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
var fallbackRequestIDCounter uint64
var randRead = rand.Read

// TraceContext carries the OpenBKN phase-one correlation context.
type TraceContext struct {
	RequestID string
	Baggage   map[string]string
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
	if requestID == "" || !IsValidBKNRequestID(requestID) {
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
	if _, err := randRead(b[:]); err != nil {
		log.Printf("bkn trace request id generation degraded: %v", err)
		counter := atomic.AddUint64(&fallbackRequestIDCounter, 1)
		return fmt.Sprintf("req_fallback_%d_%d", time.Now().UnixNano(), counter)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("req_%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func BuildTraceHeaders(ctx context.Context) map[string]string {
	headers := map[string]string{}
	traceContext, ok := GetTraceContextFromCtx(ctx)
	if ok {
		headers[HeaderBKNRequestID] = traceContext.RequestID
		headers[HeaderLegacyRequestID] = traceContext.RequestID
		if baggage := formatBaggage(traceContext.Baggage); baggage != "" {
			headers[HeaderBaggage] = baggage
		}
	}
	if traceparent := traceparentFromCtx(ctx); traceparent != "" {
		headers[HeaderTraceparent] = traceparent
	}
	return headers
}

func MergeTraceHeaders(ctx context.Context, headers map[string]string) map[string]string {
	if headers == nil {
		headers = map[string]string{}
	}
	for key, value := range BuildTraceHeaders(ctx) {
		headers[key] = value
	}
	return headers
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
