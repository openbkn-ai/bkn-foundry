// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/audit"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/directory"
)

// Context keys shared between the gates and the audit middlewares.
const (
	// ctxAuthnSubject is set by every gate the moment the bearer token verifies,
	// before any authorization check — so a 403 can still be attributed to the
	// subject that was refused.
	ctxAuthnSubject = "authn_subject"
	// ctxGateFailure names which gate refused the request: authn (no/invalid
	// token), inactive (account disabled), authz (not an administrator / not
	// an owner), permission (permission point not held).
	ctxGateFailure = "gate_failure"
	// ctxAuditRecorded is set by auditMiddleware once it has written a row, so
	// the failure recorder does not write a second one for the same request.
	ctxAuditRecorded = "audit_recorded"
)

const (
	gateAuthn      = "authn"
	gateInactive   = "inactive"
	gateAuthz      = "authz"
	gatePermission = "permission"
)

// abortGate refuses the request and records which gate did it.
func abortGate(c *gin.Context, status int, gate string) {
	c.Set(ctxGateFailure, gate)
	abortPublicError(c, status)
}

// failureLimiter throttles refusal rows per source and route. One expired
// token polled by a frontend, or one non-administrator account looping over an
// admin route, would otherwise write a chained row per request — and every
// chained append holds the chain lock, so the looping caller's request rate
// would become the ceiling for every other audit write. One row per window is
// enough to show the pattern. 401s are keyed by client address (there is no
// subject to key on); 403s are keyed by the verified subject, so the first
// refusal of every account on every route is always recorded and only its
// repeats within the window are folded.
type failureLimiter struct {
	mu     sync.Mutex
	window time.Duration
	seen   map[string]time.Time
	now    func() time.Time
}

const failureLimiterWindow = time.Minute

// failureLimiterMaxKeys bounds the map; when exceeded, expired keys are swept
// and, if still too large, the map is reset (throttling restarts, nothing
// else is affected).
const failureLimiterMaxKeys = 10000

func newFailureLimiter(window time.Duration) *failureLimiter {
	return &failureLimiter{window: window, seen: map[string]time.Time{}, now: time.Now}
}

// allow reports whether a failure for key should be recorded now.
func (l *failureLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if last, ok := l.seen[key]; ok && now.Sub(last) < l.window {
		return false
	}
	if len(l.seen) >= failureLimiterMaxKeys {
		for k, t := range l.seen {
			if now.Sub(t) >= l.window {
				delete(l.seen, k)
			}
		}
		if len(l.seen) >= failureLimiterMaxKeys {
			l.seen = map[string]time.Time{}
		}
	}
	l.seen[key] = now
	return true
}

// auditAuthFailures records the 401/403 responses produced by the gates in
// front of a token-gated group. It must be registered BEFORE those gates —
// gin runs it first and it observes their abort on the way back — and it stays
// quiet when auditMiddleware already recorded the request (a mutating request
// refused by a permission point is recorded there, with the gate noted).
//
// A refused request still carries what the caller tried to do: method, route,
// path target and the verified subject when the token was good. The body is
// not read — a refused request's payload is untrusted and the gate never saw
// it either.
func auditAuthFailures(store *audit.Store, dir *directory.Service, limiter *failureLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Settle the request id before the gates run: a refused request has
		// its response written by the gate, so a header set afterwards would
		// never reach the client. Putting it on the request as well makes the
		// mutation audit and the decision log reuse the same id, so one
		// x-request-id ties the client's view, the audit row and the decision
		// rows together.
		requestID := requestIDFromHeader(c)
		if requestID == "" {
			requestID = audit.NewID()
			c.Request.Header.Set("x-request-id", requestID)
		}
		c.Header("x-request-id", requestID)
		c.Next()
		status := c.Writer.Status()
		if status != http.StatusUnauthorized && status != http.StatusForbidden {
			return
		}
		if c.GetBool(ctxAuditRecorded) {
			return
		}
		gate := c.GetString(ctxGateFailure)
		if gate == "" {
			gate = gateAuthz
			if status == http.StatusUnauthorized {
				gate = gateAuthn
			}
		}
		actorID := c.GetString(ctxAuthnSubject)
		if limiter != nil {
			source := "ip:" + c.ClientIP()
			if status == http.StatusForbidden && actorID != "" {
				source = "sub:" + actorID
			}
			if !limiter.allow(strconv.Itoa(status) + "|" + source + "|" + c.Request.Method + "|" + c.FullPath()) {
				return
			}
		}
		resource, _ := auditTarget(c.FullPath())
		action := auditAction(c.Request.Method, c.FullPath())
		actorType := "user"
		if actorID == "" {
			actorType = "anonymous"
		}
		authMethod := "oauth"
		if bearerToken(c) == "" {
			authMethod = "none"
		}
		detail, _ := json.Marshal(map[string]any{"_gate": gate})
		if err := store.Record(c.Request.Context(), audit.Entry{
			ActorID:           actorID,
			ActorNameSnapshot: auditActorName(c.Request.Context(), dir, actorID),
			ActorType:         actorType,
			AuthMethod:        authMethod,
			RequestID:         requestID,
			SourceChannel:     "api",
			Method:            c.Request.Method,
			Resource:          resource,
			Action:            action,
			TargetID:          c.Param("id"),
			Detail:            string(detail),
			Status:            status,
			ClientIP:          c.ClientIP(),
		}); err != nil {
			slog.Error("failed to persist auth failure audit record",
				"request_id", requestID, "resource", resource, "action", action, "status", status, "error", err)
		}
	}
}
