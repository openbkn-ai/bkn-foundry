// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/audit"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/decisionlog"
)

// ctxDecisionLog is the gin context key under which withDecisionLog stores the
// decision store, so gates and handlers deep in the chain can record without
// every constructor growing a parameter.
const ctxDecisionLog = "authz_decision_log"

// Decision sources: which entry point produced the row.
const (
	decisionSourceCheck      = "check"
	decisionSourceOperations = "operations"
	decisionSourceFilter     = "resource-filter"
	decisionSourceAdmin      = "admin"
)

// basisInactiveAccount marks a deny that came from the local account state
// rather than from policy: the accessor is disabled.
const basisInactiveAccount = "inactive_account"

// withDecisionLog makes store reachable from any handler on the engine.
func withDecisionLog(store *decisionlog.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(ctxDecisionLog, store)
		c.Next()
	}
}

func decisionLogFrom(c *gin.Context) *decisionlog.Store {
	raw, ok := c.Get(ctxDecisionLog)
	if !ok {
		return nil
	}
	store, _ := raw.(*decisionlog.Store)
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

// decisionDetail renders a small JSON object for the Detail column, dropping
// it when it would not fit rather than storing a truncated fragment.
func decisionDetail(m map[string]any) string {
	b, err := json.Marshal(m)
	if err != nil || len(b) > 1000 {
		return ""
	}
	return string(b)
}

// registerDecisionReads mounts the decision-log read endpoint under the admin
// group, behind the same permission point as the audit log.
func registerDecisionReads(g *gin.RouterGroup, store *decisionlog.Store, e *authz.Enforcer) {
	// GET /authz-decisions — list decisions newest-first. Query:
	// ?accessor_id=&resource_type=&resource_id=&operation=&decision=&source=
	// &from=&to=&offset=&limit= (from/to RFC3339). -> { decisions:[...], total }
	g.GET("/authz-decisions", RequirePermission(e, "admin-audit", "view"), func(c *gin.Context) {
		f := decisionlog.Filter{
			AccessorID:   c.Query("accessor_id"),
			ResourceType: c.Query("resource_type"),
			ResourceID:   c.Query("resource_id"),
			Operation:    c.Query("operation"),
			Decision:     c.Query("decision"),
			Source:       c.Query("source"),
			Offset:       atoiDefault(c.Query("offset"), 0),
			Limit:        atoiDefault(c.Query("limit"), 0),
		}
		if !accessLogTimeFilter(c, "from", &f.From) || !accessLogTimeFilter(c, "to", &f.To) {
			return
		}
		rows, total, err := store.List(c.Request.Context(), f)
		if err != nil {
			serverError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"decisions": rows, "total": total, "dropped": store.Dropped()})
	})
}

// registerAuditChainReads mounts the chain anchor and verification endpoints
// under the admin group. They are reads and produce no audit rows themselves.
func registerAuditChainReads(g *gin.RouterGroup, store *audit.Store, e *authz.Enforcer) {
	// GET /audit-chain — the current chain head, for export to an external
	// append-only store. -> { head:{seq,row_hash,created_at}|null, unchained_rows }
	g.GET("/audit-chain", RequirePermission(e, "admin-audit", "view"), func(c *gin.Context) {
		res, err := store.Verify(c.Request.Context(), 0, 0, 1)
		if err != nil {
			serverError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"head": res.Head, "unchained_rows": res.UnchainedRows})
	})
	// GET /audit-chain/verify?from_seq=&to_seq=&limit= — re-hash the chain
	// and report the first break. -> audit.VerifyResult
	g.GET("/audit-chain/verify", RequirePermission(e, "admin-audit", "view"), func(c *gin.Context) {
		from, ok := parseSeq(c.Query("from_seq"))
		if !ok {
			replyPublicError(c, http.StatusBadRequest)
			return
		}
		to, ok := parseSeq(c.Query("to_seq"))
		if !ok {
			replyPublicError(c, http.StatusBadRequest)
			return
		}
		res, err := store.Verify(c.Request.Context(), from, to, atoiDefault(c.Query("limit"), 0))
		if err != nil {
			serverError(c, err)
			return
		}
		c.JSON(http.StatusOK, res)
	})
}

func parseSeq(v string) (uint64, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, true
	}
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}
