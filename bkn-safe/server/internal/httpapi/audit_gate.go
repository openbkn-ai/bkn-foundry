// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import "github.com/gin-gonic/gin"

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
