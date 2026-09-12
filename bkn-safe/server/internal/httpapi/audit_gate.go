// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
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

// failureLimiter throttles authentication-failure rows per (client, route).
// One expired token polled by a frontend would otherwise write a row per poll;
// one row per window per source is enough to show the pattern, and it keeps a
// credential-stuffing burst from filling the audit table faster than it can
// be read. Authorization failures (403) are never throttled: they carry a
// verified subject and are rare.
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
		if status == http.StatusUnauthorized && limiter != nil && !limiter.allow(c.ClientIP()+"|"+c.Request.Method+"|"+c.FullPath()) {
			return
		}
		requestID := requestIDFromHeader(c)
		if requestID == "" {
			requestID = audit.NewID()
		}
		c.Header("x-request-id", requestID)
		resource, _ := auditTarget(c.FullPath())
		action := auditAction(c.Request.Method, c.FullPath())
		actorID := c.GetString(ctxAuthnSubject)
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
