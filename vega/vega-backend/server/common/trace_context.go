package common

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

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
	HeaderBKNEventObservedAt  = "bkn-event-observed-at"
)

type traceContextKey string

const keyTraceContext traceContextKey = "bkn_trace_context"

var bknRequestIDRe = regexp.MustCompile(`^req_[A-Za-z0-9_-]{8,128}$`)

// TraceContext carries the OpenBKN phase-one correlation context.
type TraceContext struct {
	RequestID          string
	Baggage            map[string]string
	InteractionID      string
	OperationID        string
	CausationEventID   string
	ClaimID            string
	Attempt            int
	ObservedAt         string
	ObservedAtProvided bool
}

var businessTraceIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

func SetTraceContextToCtx(ctx context.Context, traceContext TraceContext) context.Context {
	if !IsValidBKNRequestID(traceContext.RequestID) {
		traceContext.RequestID = NewBKNRequestID()
	}
	traceContext.Baggage = sanitizeBaggage(traceContext.Baggage)
	traceContext.InteractionID = sanitizeBusinessTraceID(traceContext.InteractionID)
	traceContext.OperationID = sanitizeBusinessTraceID(traceContext.OperationID)
	traceContext.CausationEventID = sanitizeBusinessTraceID(traceContext.CausationEventID)
	traceContext.ClaimID = sanitizeBusinessTraceID(traceContext.ClaimID)
	if traceContext.Attempt < 1 || traceContext.Attempt > 1000 {
		traceContext.Attempt = 1
	}
	if _, err := time.Parse(time.RFC3339Nano, traceContext.ObservedAt); err != nil {
		traceContext.ObservedAt = time.Now().UTC().Format(time.RFC3339Nano)
		traceContext.ObservedAtProvided = false
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
	observedAt := strings.TrimSpace(getHeader(HeaderBKNEventObservedAt))
	_, observedAtErr := time.Parse(time.RFC3339Nano, observedAt)
	return TraceContext{
		RequestID:          requestID,
		Baggage:            parseBaggage(getHeader(HeaderBaggage)),
		InteractionID:      sanitizeBusinessTraceID(getHeader(HeaderBKNInteractionID)),
		OperationID:        sanitizeBusinessTraceID(getHeader(HeaderBKNOperationID)),
		CausationEventID:   sanitizeBusinessTraceID(getHeader(HeaderBKNCausationEventID)),
		ClaimID:            sanitizeBusinessTraceID(getHeader(HeaderBKNClaimID)),
		Attempt:            attempt,
		ObservedAt:         observedAt,
		ObservedAtProvided: observedAtErr == nil,
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
		if traceContext.ObservedAtProvided {
			headers[HeaderBKNEventObservedAt] = traceContext.ObservedAt
		}
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
		for _, protected := range []string{HeaderBKNInteractionID, HeaderBKNOperationID, HeaderBKNCausationEventID, HeaderBKNClaimID, HeaderBKNAttempt, HeaderBKNEventObservedAt} {
			if strings.EqualFold(key, protected) {
				delete(header, key)
				break
			}
		}
	}
}

func sanitizeBusinessTraceID(value string) string {
	value = strings.TrimSpace(value)
	if !businessTraceIDRe.MatchString(value) {
		return ""
	}
	return value
}

// BuildTraceHeadersForChildOperation forks a deterministic child operation without changing the direct cause.
func BuildTraceHeadersForChildOperation(ctx context.Context, operationName string, callOrdinal int) map[string]string {
	traceContext, ok := GetTraceContextFromCtx(ctx)
	if !ok {
		return BuildTraceHeaders(ctx)
	}
	traceContext.OperationID = childOperationID(traceContext.OperationID, operationName, traceContext.Attempt, callOrdinal)
	return BuildTraceHeaders(SetTraceContextToCtx(ctx, traceContext))
}

func childOperationID(parentOperationID, operationName string, attempt, callOrdinal int) string {
	if callOrdinal < 1 {
		callOrdinal = 1
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d|%d", parentOperationID, strings.TrimSpace(operationName), attempt, callOrdinal)))
	return "op_" + hex.EncodeToString(sum[:])
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

// MergeTraceHeadersForChildOperation merges caller headers with a deterministic child operation.
// Callers issuing the same operation more than once under one parent must increment callOrdinal.
func MergeTraceHeadersForChildOperation(ctx context.Context, headers map[string]string, operationName string, callOrdinal int) map[string]string {
	if headers == nil {
		headers = map[string]string{}
	}
	for key, value := range BuildTraceHeadersForChildOperation(ctx, operationName, callOrdinal) {
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
