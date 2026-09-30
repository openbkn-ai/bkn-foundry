// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"encoding/json"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/decisionlog"
)

// ctxDecisionLog is the gin context key under which withDecisionLog stores the
// decision store, so gates and handlers deep in the chain can record without
// every constructor growing a parameter.
const ctxDecisionLog = "authz_decision_log"

// Decision sources: which entry point produced the row.
const (
	decisionSourceCheck  = "check"
	decisionSourceFilter = "resource-filter"
	decisionSourceAdmin  = "admin"
)

// basisInactiveAccount marks a deny that came from the local account state
// rather than from policy: the accessor is disabled.
const basisInactiveAccount = "inactive_account"

// withDecisionLog makes store reachable from any handler on the engine.
func withDecisionLog(store decisionlog.Recorder) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(ctxDecisionLog, store)
		c.Next()
	}
}

func decisionLogFrom(c *gin.Context) decisionlog.Recorder {
	raw, ok := c.Get(ctxDecisionLog)
	if !ok {
		return nil
	}
	store, _ := raw.(decisionlog.Recorder)
	return store
}

// recordDecision writes one decision row for the current request. Request
// correlation (x-request-id, traceparent, client ip) is filled in here so call
// sites only state the authorization facts. Safe when no store is mounted.
func recordDecision(c *gin.Context, e decisionlog.Entry) {
	store := decisionLogFrom(c)
	if store == nil {
		return
	}
	if e.RequestID == "" {
		e.RequestID = requestIDFromHeader(c)
	}
	if e.TraceID == "" {
		e.TraceID = traceIDFromHeader(c)
	}
	if e.Method == "" {
		e.Method = c.Request.Method
	}
	if e.VerifiedActorID == "" {
		e.VerifiedActorID = c.GetString(ctxAccessorID)
		if e.VerifiedActorID == "" && e.Source == decisionSourceAdmin {
			// The admin gate records its decision after token verification,
			// before ctxAccessorID is set for downstream handlers.
			e.VerifiedActorID = c.GetString(ctxAuthnSubject)
		}
		if e.VerifiedActorID == "" && (e.Source == decisionSourceCheck || e.Source == decisionSourceFilter) && decisionlog.IsSafeActorID(e.AccessorID) {
			// The internal authorization face receives the end-user identity as
			// accessor_id after the calling service resolves its own boundary.
			// It is the subject whose access was decided, so preserve it as the
			// audit actor instead of manufacturing an anonymous caller.
			e.VerifiedActorID = e.AccessorID
		}
	}
	if e.ClientIP == "" {
		e.ClientIP = c.ClientIP()
	}
	store.Record(e)
}

// recordEvaluation records an enforcer evaluation for one (resource, op).
func recordEvaluation(c *gin.Context, source, accessorID, resourceType, resourceID, op string, ev authz.Evaluation, detail string) {
	recordDecision(c, decisionlog.Entry{
		AccessorID: accessorID, ResourceType: resourceType, ResourceID: resourceID, Operation: op,
		Scope: string(ev.Scope), Decision: string(ev.Decision), Basis: string(ev.Basis),
		DeniedRequirement: ev.DeniedRequirement, Source: source, Detail: detail,
	})
}

// recordInactiveDeny records the deny that a disabled account receives before
// any policy is consulted.
func recordInactiveDeny(c *gin.Context, source, accessorID, resourceType, resourceID, op string) {
	recordDecision(c, decisionlog.Entry{
		AccessorID: accessorID, ResourceType: resourceType, ResourceID: resourceID, Operation: op,
		Scope: string(authz.ScopeEffective), Decision: decisionlog.DecisionDeny, Basis: basisInactiveAccount,
		Source: source,
	})
}

// traceIDFromHeader extracts the trace id of a W3C traceparent header
// (00-<32 hex trace id>-<16 hex span id>-<2 hex flags>), or "".
func traceIDFromHeader(c *gin.Context) string {
	parts := strings.Split(strings.TrimSpace(c.GetHeader("traceparent")), "-")
	if len(parts) < 3 || len(parts[1]) != 32 {
		return ""
	}
	for _, r := range parts[1] {
		if !isHexRune(r) {
			return ""
		}
	}
	return strings.ToLower(parts[1])
}

func isHexRune(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

// decisionDetail bounds the in-process summary; Kafka records omit it because
// it can contain untrusted request input.
func decisionDetail(m map[string]any) string {
	b, err := json.Marshal(m)
	if err != nil || len(b) > 1000 {
		return ""
	}
	return string(b)
}
