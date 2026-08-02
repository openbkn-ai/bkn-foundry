package common

import (
	"context"
	"strings"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"go.opentelemetry.io/otel/trace"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

func TestResponseFormatContextHelpers(t *testing.T) {
	convey.Convey("SetResponseFormatToCtx and GetResponseFormatFromCtx", t, func() {
		type responseFormat string

		ctx := context.Background()
		ctx = SetResponseFormatToCtx(ctx, responseFormat("toon"))

		v, ok := GetResponseFormatFromCtx(ctx)
		convey.So(ok, convey.ShouldBeTrue)
		convey.So(v, convey.ShouldEqual, responseFormat("toon"))
	})
}

func TestIsPublicAPIFromCtx(t *testing.T) {
	convey.Convey("SetPublicAPIToCtx and IsPublicAPIFromCtx", t, func() {
		ctx := context.Background()
		convey.So(IsPublicAPIFromCtx(ctx), convey.ShouldBeFalse)

		ctx = SetPublicAPIToCtx(ctx, true)
		convey.So(IsPublicAPIFromCtx(ctx), convey.ShouldBeTrue)
	})
}

func TestGetHeaderFromCtx(t *testing.T) {
	convey.Convey("GetHeaderFromCtx returns account headers when auth context exists", t, func() {
		ctx := context.Background()
		authCtx := &interfaces.AccountAuthContext{
			AccountID:   "user-1",
			AccountType: interfaces.AccessorType("tenant"),
		}
		ctx = SetAccountAuthContextToCtx(ctx, authCtx)

		header := GetHeaderFromCtx(ctx)
		convey.So(header[string(interfaces.HeaderXAccountID)], convey.ShouldEqual, "user-1")
		convey.So(header[string(interfaces.HeaderXAccountType)], convey.ShouldEqual, "tenant")
	})
}

func TestTraceContextHelpers(t *testing.T) {
	convey.Convey("SetTraceContextToCtx preserves a valid request id and sanitizes baggage", t, func() {
		ctx := SetTraceContextToCtx(context.Background(), TraceContext{
			RequestID: "req_01JZVALIDREQUESTID000000000",
			Baggage: map[string]string{
				"bkn.account.type": "service",
				"bkn.runtime.env":  "test",
				"bkn.account.id":   "user-1",
				"prompt":           "raw prompt",
			},
		})

		traceCtx, ok := GetTraceContextFromCtx(ctx)
		convey.So(ok, convey.ShouldBeTrue)
		convey.So(traceCtx.RequestID, convey.ShouldEqual, "req_01JZVALIDREQUESTID000000000")
		convey.So(traceCtx.Baggage, convey.ShouldResemble, map[string]string{
			"bkn.runtime.env": "test",
		})
	})

	convey.Convey("SetTraceContextToCtx generates a request id when missing or invalid", t, func() {
		ctx := SetTraceContextToCtx(context.Background(), TraceContext{RequestID: "bad id"})

		traceCtx, ok := GetTraceContextFromCtx(ctx)
		convey.So(ok, convey.ShouldBeTrue)
		convey.So(traceCtx.RequestID, convey.ShouldStartWith, "req_")
		convey.So(IsValidBKNRequestID(traceCtx.RequestID), convey.ShouldBeTrue)
	})
}

func TestGetHeaderFromCtxPropagatesTraceContext(t *testing.T) {
	convey.Convey("GetHeaderFromCtx returns bkn request id, legacy request id, traceparent, and allowed baggage", t, func() {
		traceID := trace.TraceID{0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x20, 0x21, 0x22, 0x23, 0x24, 0x25}
		spanID := trace.SpanID{0x30, 0x31, 0x32, 0x33, 0x34, 0x35, 0x36, 0x37}
		spanCtx := trace.NewSpanContext(trace.SpanContextConfig{
			TraceID:    traceID,
			SpanID:     spanID,
			TraceFlags: trace.FlagsSampled,
			Remote:     true,
		})
		ctx := trace.ContextWithSpanContext(context.Background(), spanCtx)
		ctx = SetTraceContextToCtx(ctx, TraceContext{
			RequestID: "req_01JZVALIDREQUESTID000000001",
			Baggage: map[string]string{
				"bkn.account.type": "service",
				"bkn.account.id":   "user-1",
			},
		})
		ctx = SetAccountAuthContextToCtx(ctx, &interfaces.AccountAuthContext{
			AccountID:   "user-1",
			AccountType: interfaces.AccessorType("tenant"),
		})

		header := GetHeaderFromCtx(ctx)
		convey.So(header[HeaderBKNRequestID], convey.ShouldEqual, "req_01JZVALIDREQUESTID000000001")
		convey.So(header[HeaderLegacyRequestID], convey.ShouldEqual, "req_01JZVALIDREQUESTID000000001")
		convey.So(header[HeaderTraceparent], convey.ShouldEqual, "00-10111213141516171819202122232425-3031323334353637-01")
		convey.So(header[HeaderBaggage], convey.ShouldEqual, "bkn.account.type=tenant")
	})

	convey.Convey("GetHeaderFromCtx derives account baggage from trusted auth context", t, func() {
		ctx := SetTraceContextToCtx(context.Background(), TraceContext{
			RequestID: "req_01JZVALIDREQUESTID000000004",
			Baggage: map[string]string{
				"bkn.account.type": "admin",
				"bkn.runtime.env":  "test",
			},
		})
		ctx = SetAccountAuthContextToCtx(ctx, &interfaces.AccountAuthContext{
			AccountID:   "user-1",
			AccountType: interfaces.AccessorType("service"),
		})

		header := GetHeaderFromCtx(ctx)
		convey.So(header[HeaderBaggage], convey.ShouldEqual, "bkn.account.type=service,bkn.runtime.env=test")
	})
}

func TestBusinessCausalityHeadersAreValidatedAndPropagated(t *testing.T) {
	convey.Convey("caller-owned conversation id is propagated without being generated", t, func() {
		headers := map[string]string{
			HeaderBKNRequestID:    "req_01JZVALIDREQUESTID000000009",
			"bkn-conversation-id": "agent:thread_supply_chain",
		}
		ctx := SetTraceContextToCtx(context.Background(), TraceContextFromHeaders(func(key string) string {
			return headers[key]
		}))

		header := GetHeaderFromCtx(ctx)
		convey.So(header["bkn-conversation-id"], convey.ShouldEqual, "agent:thread_supply_chain")

		degraded := GetHeaderFromCtx(SetTraceContextToCtx(context.Background(), TraceContext{
			RequestID: "req_01JZVALIDREQUESTID000000010",
		}))
		_, generated := degraded["bkn-conversation-id"]
		convey.So(generated, convey.ShouldBeFalse)
	})

	convey.Convey("valid business causality is propagated", t, func() {
		ctx := SetTraceContextToCtx(context.Background(), TraceContext{
			RequestID:          "req_01JZVALIDREQUESTID000000005",
			TenantID:           "tenant-supply-chain",
			BusinessDomain:     "domain-sales-001",
			InteractionID:      "third-party-interaction-0001",
			OperationID:        "context-retrieval-0001",
			CausationEventID:   "agent-tool-called-0001",
			ClaimID:            "agent-answer-0001",
			Attempt:            2,
			ObservedAt:         "2026-07-25T08:00:00Z",
			ObservedAtProvided: true,
		})

		header := GetHeaderFromCtx(ctx)
		convey.So(header[HeaderTenantID], convey.ShouldEqual, "tenant-supply-chain")
		convey.So(header[HeaderBusinessDomain], convey.ShouldEqual, "domain-sales-001")
		convey.So(header[HeaderBKNInteractionID], convey.ShouldEqual, "third-party-interaction-0001")
		convey.So(header[HeaderBKNOperationID], convey.ShouldEqual, "context-retrieval-0001")
		convey.So(header[HeaderBKNCausationEventID], convey.ShouldEqual, "agent-tool-called-0001")
		convey.So(header[HeaderBKNClaimID], convey.ShouldEqual, "agent-answer-0001")
		convey.So(header[HeaderBKNAttempt], convey.ShouldEqual, "2")
		convey.So(header[HeaderBaggage], convey.ShouldContainSubstring, "business_domain=domain-sales-001")
	})

	convey.Convey("child operation is deterministic and does not reuse its parent operation", t, func() {
		ctx := SetTraceContextToCtx(context.Background(), TraceContext{
			RequestID: "req_01JZVALIDREQUESTID000000008", InteractionID: "interaction-1",
			OperationID: "parent-operation", CausationEventID: "parent-event", Attempt: 2,
		})
		first := GetHeaderForChildOperation(ctx, "ontology.object.query", 1)
		second := GetHeaderForChildOperation(ctx, "ontology.object.query", 1)
		third := GetHeaderForChildOperation(ctx, "ontology.object.query", 2)
		convey.So(first[HeaderBKNOperationID], convey.ShouldNotEqual, "parent-operation")
		convey.So(first[HeaderBKNOperationID], convey.ShouldEqual, second[HeaderBKNOperationID])
		convey.So(first[HeaderBKNOperationID], convey.ShouldNotEqual, third[HeaderBKNOperationID])
		convey.So(first[HeaderBKNCausationEventID], convey.ShouldEqual, "parent-event")
		convey.So(first[HeaderBKNAttempt], convey.ShouldEqual, "2")
	})

	convey.Convey("invalid inbound business causality is removed at the boundary", t, func() {
		headers := map[string]string{
			HeaderBKNRequestID:        "req_01JZVALIDREQUESTID000000006",
			HeaderBKNInteractionID:    "../../interaction",
			HeaderBKNOperationID:      "raw sql select * from users",
			HeaderBKNCausationEventID: "evt ok with spaces",
			HeaderBKNClaimID:          "claim<script>",
			HeaderBKNAttempt:          "-1",
		}
		ctx := SetTraceContextToCtx(context.Background(), TraceContextFromHeaders(func(key string) string { return headers[key] }))
		traceCtx, ok := GetTraceContextFromCtx(ctx)
		convey.So(ok, convey.ShouldBeTrue)
		convey.So(traceCtx.InteractionID, convey.ShouldBeEmpty)
		convey.So(traceCtx.OperationID, convey.ShouldBeEmpty)
		convey.So(traceCtx.CausationEventID, convey.ShouldBeEmpty)
		convey.So(traceCtx.ClaimID, convey.ShouldBeEmpty)
		convey.So(traceCtx.Attempt, convey.ShouldEqual, 1)
	})

	convey.Convey("business causality is stripped before an untrusted outbound hop", t, func() {
		header := map[string]string{
			HeaderTraceparent:         "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
			HeaderBKNRequestID:        "req_01JZVALIDREQUESTID000000007",
			"bkn-conversation-id":     "agent:thread_supply_chain",
			HeaderBKNInteractionID:    "int_business_trace_0001",
			HeaderBKNOperationID:      "op_context_retrieval_0001",
			HeaderBKNCausationEventID: "evt_agent_tool_called_0001",
			HeaderBKNClaimID:          "claim_agent_answer_0001",
			HeaderBKNAttempt:          "2",
		}
		StripBusinessTraceHeaders(header)
		convey.So(header[HeaderTraceparent], convey.ShouldNotBeEmpty)
		convey.So(header[HeaderBKNRequestID], convey.ShouldNotBeEmpty)
		convey.So(header["bkn-conversation-id"], convey.ShouldBeEmpty)
		convey.So(header[HeaderBKNInteractionID], convey.ShouldBeEmpty)
		convey.So(header[HeaderBKNOperationID], convey.ShouldBeEmpty)
		convey.So(header[HeaderBKNCausationEventID], convey.ShouldBeEmpty)
		convey.So(header[HeaderBKNClaimID], convey.ShouldBeEmpty)
		convey.So(header[HeaderBKNAttempt], convey.ShouldBeEmpty)
	})
}

func TestTraceContextUsesConfiguredTenantOnlyWhenInboundTenantIsMissing(t *testing.T) {
	t.Setenv("BKN_TRACE_DEFAULT_TENANT_ID", "openbkn-local")
	domainOnly := TraceContextFromHeaders(func(key string) string {
		if key == HeaderBusinessDomain {
			return "bd_public"
		}
		return ""
	})
	if domainOnly.TenantID != "openbkn-local" {
		t.Fatalf("default tenant = %q, want openbkn-local", domainOnly.TenantID)
	}
	trustedTenant := TraceContextFromHeaders(func(key string) string {
		switch key {
		case HeaderTenantID:
			return "tenant-from-gateway"
		case HeaderBusinessDomain:
			return "bd_public"
		default:
			return ""
		}
	})
	if trustedTenant.TenantID != "tenant-from-gateway" {
		t.Fatalf("inbound tenant was overwritten: %q", trustedTenant.TenantID)
	}
}

func TestCallerCorrelationIDsAreValidatedWithoutGeneration(t *testing.T) {
	convey.Convey("valid caller ids are propagated and invalid ids are dropped", t, func() {
		headers := map[string]string{
			HeaderBKNRequestID:      "req_01JZVALIDREQUESTID000000010",
			HeaderBKNConversationID: " agent:thread_abc ",
			HeaderBKNInteractionID:  "itr_2026072701",
		}
		traceCtx := TraceContextFromHeaders(func(key string) string { return headers[key] })
		convey.So(traceCtx.ConversationID, convey.ShouldEqual, "agent:thread_abc")
		convey.So(traceCtx.InteractionID, convey.ShouldEqual, "itr_2026072701")

		invalid := SetTraceContextToCtx(context.Background(), TraceContext{
			RequestID:      "req_01JZVALIDREQUESTID000000013",
			ConversationID: "bad id with spaces",
			InteractionID:  strings.Repeat("a", 129),
		})
		invalidCtx, ok := GetTraceContextFromCtx(invalid)
		convey.So(ok, convey.ShouldBeTrue)
		convey.So(invalidCtx.ConversationID, convey.ShouldBeEmpty)
		convey.So(invalidCtx.InteractionID, convey.ShouldBeEmpty)

		degraded := GetHeaderFromCtx(SetTraceContextToCtx(context.Background(), TraceContext{
			RequestID: "req_01JZVALIDREQUESTID000000015",
		}))
		_, hasConversation := degraded[HeaderBKNConversationID]
		_, hasInteraction := degraded[HeaderBKNInteractionID]
		convey.So(hasConversation, convey.ShouldBeFalse)
		convey.So(hasInteraction, convey.ShouldBeFalse)
	})
}
