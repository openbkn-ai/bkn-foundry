// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package adminwrite

import "github.com/gin-gonic/gin"

// AuditOperation describes the committed business operation owned by an
// extension. Names must be resolved from the saved object, never synthesized
// from an ID. Core still verifies the actor and successful response before
// publishing exactly one event. Suppress marks read-only POST operations.
type AuditOperation struct {
	Action      string
	TargetID    string
	TargetName  string
	TargetType  string // canonical type for extension-owned resources; core types cannot be overridden
	SnapshotErr error
	Suppress    bool
}

// SetAuditOperation supplies the extension's object snapshot to core's existing
// administration audit middleware; it does not publish a second audit event.
func SetAuditOperation(c *gin.Context, operation AuditOperation) {
	c.Set("audit_operation", operation)
}
