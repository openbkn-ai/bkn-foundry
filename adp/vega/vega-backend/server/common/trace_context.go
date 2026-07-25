package common

import (
	"context"
	"crypto/rand"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel/trace"
)

const (
	HeaderTraceparent         = "traceparent"
	HeaderBKNRequestID        = "bkn-request-id"
	HeaderLegacyRequestID     = "x-request-id"
	HeaderBaggage             = "baggage"
	HeaderBKNInteractionID    = "bkn-interaction-id"
	HeaderBKNOperationID      = "bkn-operation-id"
	HeaderBKNCausationEventID = "bkn-causation-event-id"
	HeaderBKNClaimID          = "bkn-claim-id"
	HeaderBKNAttempt          = "bkn-attempt"
)

type traceContextKey string

const keyTraceContext traceContextKey = "bkn_trace_context"

var bknRequestIDRe = regexp.MustCompile(`^req_[A-Za-z0-9_-]{8,128}$`)

// TraceContext carries the OpenBKN phase-one correlation context.
type TraceContext struct {
	RequestID        string
	Baggage          map[string]string
	InteractionID    string
	OperationID      string
	CausationEventID string
	ClaimID          string
	Attempt          int
}

var businessTraceIDRe = regexp.MustCompile(`^(int|op|evt|claim)_[A-Za-z0-9_-]{8,128}$`)

func SetTraceContextToCtx(ctx context.Context, traceContext TraceContext) context.Context {
	if !IsValidBKNRequestID(traceContext.RequestID) {
		traceContext.RequestID = NewBKNRequestID()
	}
	traceContext.Baggage = sanitizeBaggage(traceContext.Baggage)
	traceContext.InteractionID = sanitizeBusinessTraceID(traceContext.InteractionID, "int_")
	traceContext.OperationID = sanitizeBusinessTraceID(traceContext.OperationID, "op_")
	traceContext.CausationEventID = sanitizeBusinessTraceID(traceContext.CausationEventID, "evt_")
	traceContext.ClaimID = sanitizeBusinessTraceID(traceContext.ClaimID, "claim_")
	if traceContext.Attempt < 1 || traceContext.Attempt > 1000 {
		traceContext.Attempt = 1
	}
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
	attempt, _ := strconv.Atoi(strings.TrimSpace(getHeader(HeaderBKNAttempt)))
	if attempt < 1 || attempt > 1000 {
		attempt = 1
	}
	return TraceContext{
		RequestID:        requestID,
		Baggage:          parseBaggage(getHeader(HeaderBaggage)),
		InteractionID:    sanitizeBusinessTraceID(getHeader(HeaderBKNInteractionID), "int_"),
		OperationID:      sanitizeBusinessTraceID(getHeader(HeaderBKNOperationID), "op_"),
		CausationEventID: sanitizeBusinessTraceID(getHeader(HeaderBKNCausationEventID), "evt_"),
		ClaimID:          sanitizeBusinessTraceID(getHeader(HeaderBKNClaimID), "claim_"),
		Attempt:          attempt,
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

func BuildTraceHeaders(ctx context.Context) map[string]string {
	headers := map[string]string{}
	traceContext, ok := GetTraceContextFromCtx(ctx)
	if ok {
		headers[HeaderBKNRequestID] = traceContext.RequestID
		headers[HeaderLegacyRequestID] = traceContext.RequestID
		if baggage := formatBaggage(traceContext.Baggage); baggage != "" {
			headers[HeaderBaggage] = baggage
		}
		setBusinessTraceHeaders(headers, traceContext)
	}
	if traceparent := traceparentFromCtx(ctx); traceparent != "" {
		headers[HeaderTraceparent] = traceparent
	}
	return headers
}

func setBusinessTraceHeaders(header map[string]string, traceContext TraceContext) {
	for key, value := range map[string]string{
		HeaderBKNInteractionID:    traceContext.InteractionID,
		HeaderBKNOperationID:      traceContext.OperationID,
		HeaderBKNCausationEventID: traceContext.CausationEventID,
		HeaderBKNClaimID:          traceContext.ClaimID,
	} {
		if value != "" {
			header[key] = value
		}
	}
	if traceContext.OperationID != "" && traceContext.Attempt > 0 {
		header[HeaderBKNAttempt] = strconv.Itoa(traceContext.Attempt)
	}
}

// StripBusinessTraceHeaders removes OpenBKN-only causality before an untrusted outbound hop.
func StripBusinessTraceHeaders(header map[string]string) {
	for key := range header {
		for _, protected := range []string{HeaderBKNInteractionID, HeaderBKNOperationID, HeaderBKNCausationEventID, HeaderBKNClaimID, HeaderBKNAttempt} {
			if strings.EqualFold(key, protected) {
				delete(header, key)
				break
			}
		}
	}
}

func sanitizeBusinessTraceID(value, prefix string) string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, prefix) || !businessTraceIDRe.MatchString(value) {
		return ""
	}
	return value
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
