// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package permobject

import (
	"context"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/licverify"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
)

// SubjectType is the shared Core/EE vocabulary produced by authoritative
// directory classification. It deliberately remains a string alias so an EE
// provider can adopt the exported constants without a cross-repository source
// compatibility break.
type SubjectType = string

const (
	SubjectTypeUnknown    = "unknown"
	SubjectTypeUser       = "user"
	SubjectTypeRole       = "role"
	SubjectTypeDepartment = "department"
)

// InventoryEntry is the non-sensitive management projection of one historical
// Enterprise object rule. The private implementation keeps the rule payload;
// Core receives only fields required to classify, audit and revoke it.
// RuntimeEligible is the final management result after authoritative subject
// classification, not merely the intrinsic validity of the persisted row.
type InventoryEntry struct {
	GrantID         string      `json:"grant_id"`
	RuleID          string      `json:"rule_id"`
	AccessorID      string      `json:"accessor_id"`
	SubjectType     SubjectType `json:"subject_type"`
	ResourceType    string      `json:"resource_type"`
	ResourceID      string      `json:"resource_id"`
	Operation       string      `json:"operation"`
	Effect          string      `json:"effect"`
	ExpiresAt       *time.Time  `json:"expires_at,omitempty"`
	Classification  string      `json:"classification"`
	ActivationState string      `json:"activation_state"`
	RuntimeEligible bool        `json:"runtime_eligible"`
	InactiveReason  string      `json:"inactive_reason,omitempty"`
}

// Manager is implemented by openbkn-ee. No create or update operation is
// present: this compatibility surface is deliberately read/revoke-only.
type Manager interface {
	Inventory(ctx context.Context, now time.Time) ([]InventoryEntry, error)
	Revoke(ctx context.Context, grantID, operatorID, reason string, revokedAt time.Time) error
}

var management atomic.Value // Manager
var managementMinEdition licverify.Edition

// RegisterManager plugs the Enterprise lifecycle manager into the Core-owned
// admin route during assembly.
func RegisterManager(min licverify.Edition, manager Manager) {
	if manager == nil {
		panic("permobject: RegisterManager(nil)")
	}
	if management.Load() != nil {
		panic("permobject: manager already registered")
	}
	if load() == nil || minEdition == "" {
		panic("permobject: authorizer must be registered before manager")
	}
	if min != minEdition {
		panic("permobject: manager and authorizer minimum editions differ")
	}
	entitlement.MustBeAssembling("permobject management")
	managementMinEdition = min
	management.Store(manager)
}

func ManagementRegistered() bool { return management.Load() != nil }

func ManagementGate() gin.HandlerFunc {
	min := managementMinEdition
	return func(c *gin.Context) {
		if min != "" && entitlement.AtLeast(min) {
			c.Next()
			return
		}
		slog.Info("permobject: management route hidden, licence below the required tier",
			"capability", Capability, "edition", string(entitlement.Current()),
			"method", c.Request.Method, "path", c.Request.URL.Path)
		c.Abort()
		c.Data(http.StatusNotFound, gin.MIMEPlain, []byte("404 page not found"))
	}
}

func Inventory(ctx context.Context, now time.Time) ([]InventoryEntry, error) {
	manager, _ := management.Load().(Manager)
	return manager.Inventory(ctx, now)
}

func Revoke(ctx context.Context, grantID, operatorID, reason string, revokedAt time.Time) error {
	manager, _ := management.Load().(Manager)
	return manager.Revoke(ctx, grantID, operatorID, reason, revokedAt)
}

func resetManagement() {
	management = atomic.Value{}
	managementMinEdition = ""
}
