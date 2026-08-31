// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package common

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"go.opentelemetry.io/otel/trace"
)

const (
	HeaderTraceparent           = "traceparent"
	HeaderBKNRequestID          = "bkn-request-id"
	HeaderLegacyRequestID       = "x-request-id"
	HeaderBaggage               = "baggage"
	HeaderBKNConversationID     = "bkn-conversation-id"
	HeaderBKNInteractionID      = "bkn-interaction-id"
	HeaderBKNOperationID        = "bkn-operation-id"
	HeaderBKNReceiptID          = "bkn-receipt-id"
	HeaderBKNClientInvocationID = "X-OpenBKN-Client-Invocation-Id"
	HeaderBKNCausationEventID   = "bkn-causation-event-id"
	HeaderBKNClaimID            = "bkn-claim-id"
	HeaderBKNAttempt            = "bkn-attempt"
	HeaderBKNEventObservedAt    = "bkn-event-observed-at"
)

type traceContextKey string

const keyTraceContext traceContextKey = "bkn_trace_context"

type applicationDisplayNameKey struct{}

func SetApplicationDisplayNameToCtx(ctx context.Context, name string) context.Context {
	name = strings.TrimSpace(name)
	if len(name) > 128 {
		name = name[:128]
	}
	return context.WithValue(ctx, applicationDisplayNameKey{}, name)
}

func GetApplicationDisplayNameFromCtx(ctx context.Context) (string, bool) {
	name, ok := ctx.Value(applicationDisplayNameKey{}).(string)
	return name, ok && name != ""
}

var bknRequestIDRe = regexp.MustCompile(`^req_[A-Za-z0-9_-]{8,128}$`)

// TraceContext carries OpenBKN correlation and business causality context.
// ConversationID and InteractionID are caller-owned correlation labels. This
// service validates and propagates them, but never creates or infers them.
type TraceContext struct {
	RequestID          string
	Baggage            map[string]string
	ConversationID     string
	InteractionID      string
	OperationID        string
	ToolName           string
	CausationEventID   string
	ClaimID            string
	Attempt            int
	ObservedAt         string
	ObservedAtProvided bool
}

var businessTraceIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// GetLanguageFromCtx Gets the language setting from context.
func GetLanguageFromCtx(ctx context.Context) Language {
	return GetLanguageByCtx(ctx)
}

// SetLanguageToCtx sets the language to context.
func SetLanguageToCtx(ctx context.Context, languageInfo Language) context.Context {
	return SetLanguageByCtx(ctx, languageInfo)
}

func SetPublicAPIToCtx(ctx context.Context, isPublic bool) context.Context {
	return context.WithValue(ctx, interfaces.IsPublic, isPublic)
}

// IsPublicAPIFromCtx determines whether it is a public API.
func IsPublicAPIFromCtx(ctx context.Context) bool {
	if isPublic, ok := ctx.Value(interfaces.IsPublic).(bool); ok {
		return isPublic
	}
	return false
}

// SetRawTokenToCtx saves the caller's raw bearer token.
//
// TokenInfo is the introspection result and does not contain the original text, and PTC's run_code must bring the caller's own token to.
// Downstream: Any code submitted by the caller is executed in the sandbox, and the authorization determination must be left on the public side of the execution factory.
// (execute permission on operator types, see #345). Switching to the server identity to open the internal interface is equivalent to changing this.
// Check and wash out, any account that can connect to MCP has obtained the sandbox code execution capability.
func SetRawTokenToCtx(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, interfaces.KeyToken, token)
}

// GetRawTokenFromCtx Gets the caller's original bearer token.
func GetRawTokenFromCtx(ctx context.Context) (string, bool) {
	token, ok := ctx.Value(interfaces.KeyToken).(string)
	return token, ok && token != ""
}

// SetAccountAuthContextToCtx sets the account authentication context to context.
func SetAccountAuthContextToCtx(ctx context.Context, authContext *interfaces.AccountAuthContext) context.Context {
	return context.WithValue(ctx, interfaces.KeyAccountAuthContext, authContext)
}

func GetAccountAuthContextFromCtx(ctx context.Context) (*interfaces.AccountAuthContext, bool) {
	authContext, ok := ctx.Value(interfaces.KeyAccountAuthContext).(*interfaces.AccountAuthContext)
	return authContext, ok
}

// GetTokenInfoFromCtx Gets token information from context.
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

// SetResponseFormatToCtx sets the response format to context (for HTTP serialization exit)
func SetResponseFormatToCtx(ctx context.Context, format interface{}) context.Context {
	return context.WithValue(ctx, interfaces.KeyResponseFormat, format)
}

// GetResponseFormatFromCtx gets the response format from context, and returns nil if it does not exist (the caller processes it by default json)
func GetResponseFormatFromCtx(ctx context.Context) (interface{}, bool) {
	v := ctx.Value(interfaces.KeyResponseFormat)
	return v, v != nil
}

func SetTraceContextToCtx(ctx context.Context, traceContext TraceContext) context.Context {
	if !IsValidBKNRequestID(traceContext.RequestID) {
		traceContext.RequestID = NewBKNRequestID()
	}
	traceContext.Baggage = sanitizeBaggage(traceContext.Baggage)
	traceContext.ConversationID = sanitizeBusinessTraceID(traceContext.ConversationID)
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

// SetAuthoritativeObservedAtIfMissing uses a stable timestamp returned by a
// trusted OpenBKN lifecycle resource without overriding propagated replay time.
func SetAuthoritativeObservedAtIfMissing(ctx context.Context, observedAt time.Time) context.Context {
	traceContext, ok := GetTraceContextFromCtx(ctx)
	if !ok || traceContext.ObservedAtProvided || observedAt.IsZero() {
		return ctx
	}
	traceContext.ObservedAt = observedAt.UTC().Format(time.RFC3339Nano)
	traceContext.ObservedAtProvided = true
	return SetTraceContextToCtx(ctx, traceContext)
}

func GetTraceContextFromCtx(ctx context.Context) (TraceContext, bool) {
	traceContext, ok := ctx.Value(keyTraceContext).(TraceContext)
	return traceContext, ok
}

// CopyRequestScopedValues carries this service's per-request values from the
// HTTP request context onto another context, keeping that context's own values
// intact. The MCP transport builds a context holding the client session before
// it hands control to this service; replacing that context wholesale would drop
// the session, so the values move in this direction instead.
func CopyRequestScopedValues(from, onto context.Context) context.Context {
	if from == nil {
		return onto
	}
	if onto == nil {
		return from
	}
	for _, key := range []any{
		keyTraceContext,
		sharedrest.LanguageKey,
		interfaces.KeyAccountAuthContext,
		interfaces.KeyResponseFormat,
		interfaces.IsPublic,
		// PTC's run_code is executed in the MCP session context and cannot obtain the gin request context;
		// If you omit this item, the tool side will not be able to get the caller token and can only downgrade to the server identity.
		interfaces.KeyToken,
	} {
		if value := from.Value(key); value != nil {
			onto = context.WithValue(onto, key, value)
		}
	}
	if spanContext := trace.SpanContextFromContext(from); spanContext.IsValid() {
		onto = trace.ContextWithSpanContext(onto, spanContext)
	}
	return onto
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
		ConversationID:     sanitizeBusinessTraceID(getHeader(HeaderBKNConversationID)),
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

func IsValidCorrelationID(id string) bool {
	return businessTraceIDRe.MatchString(strings.TrimSpace(id))
}

func NewBKNRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		log.Printf("bkn trace request id generation degraded: %v", err)
		return "req_fallback"
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("req_%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// GetHeaderFromCtx When requesting the external interface, obtain the Header parameter from the context and pass it.
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
		if baggage := formatBaggage(outboundBaggage(traceContext.Baggage, authContext)); baggage != "" {
			header[HeaderBaggage] = baggage
		}
		setBusinessTraceHeaders(header, traceContext)
		if traceContext.ObservedAtProvided {
			header[HeaderBKNEventObservedAt] = traceContext.ObservedAt
		}
	}
	if traceparent := traceparentFromCtx(ctx); traceparent != "" {
		header[HeaderTraceparent] = traceparent
	}
	return
}

func setBusinessTraceHeaders(header map[string]string, traceContext TraceContext) {
	for key, value := range map[string]string{
		HeaderBKNConversationID:   traceContext.ConversationID,
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
		for _, protected := range []string{
			HeaderBKNConversationID,
			HeaderBKNInteractionID,
			HeaderBKNOperationID,
			HeaderBKNCausationEventID,
			HeaderBKNClaimID,
			HeaderBKNAttempt,
			HeaderBKNEventObservedAt} {
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

// GetHeaderForChildOperation forks a deterministic child operation without changing the direct cause.
func GetHeaderForChildOperation(ctx context.Context, operationName string, callOrdinal int) map[string]string {
	traceContext, ok := GetTraceContextFromCtx(ctx)
	if !ok {
		return GetHeaderFromCtx(ctx)
	}
	traceContext.OperationID = childOperationID(traceContext.OperationID, operationName, traceContext.Attempt, callOrdinal)
	return GetHeaderFromCtx(SetTraceContextToCtx(ctx, traceContext))
}

func childOperationID(parentOperationID, operationName string, attempt, callOrdinal int) string {
	if callOrdinal < 1 {
		callOrdinal = 1
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d|%d", parentOperationID, strings.TrimSpace(operationName), attempt, callOrdinal)))
	return "op_" + hex.EncodeToString(sum[:])
}

func sanitizeBaggage(baggage map[string]string) map[string]string {
	if len(baggage) == 0 {
		return nil
	}
	cleaned := map[string]string{}
	for key, value := range baggage {
		switch key {
		case "bkn.runtime.env":
			cleaned[key] = value
		}
	}
	if len(cleaned) == 0 {
		return nil
	}
	return cleaned
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func outboundBaggage(baggage map[string]string, authContext *interfaces.AccountAuthContext) map[string]string {
	cleaned := sanitizeBaggage(baggage)
	if authContext == nil || strings.TrimSpace(string(authContext.AccountType)) == "" {
		return cleaned
	}
	if cleaned == nil {
		cleaned = map[string]string{}
	}
	cleaned["bkn.account.type"] = strings.TrimSpace(string(authContext.AccountType))
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
