// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package rowfilter

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/licverify"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
)

// PublishedDataProperty is the only model detail handed to the EE management
// extension. It exposes published data properties and their verified exact
// filter capability, never raw mappings or backend expressions.
type PublishedDataProperty struct {
	DisplayName     string    `json:"display_name"`
	Type            ValueType `json:"type"`
	ExactFilterable bool      `json:"exact_filterable"`
}

type PublishedObjectType struct {
	ObjectTypeRef string                           `json:"object_type_ref"`
	Published     bool                             `json:"published"`
	Properties    map[string]PublishedDataProperty `json:"properties"`
}

// ManagementServices is Core-owned. All identity, platform administration
// authorization, caller expansion and published-model capability resolution
// happen here; the EE handler never accepts any of them from client input.
type ManagementServices interface {
	AuthorizeUserRead(context.Context, string) (bool, error)
	AuthorizeUserWrite(context.Context, string) (bool, error)
	AuthorizeRoleRead(context.Context, string) (bool, error)
	AuthorizeRoleWrite(context.Context, string) (bool, error)
	ResolveCaller(context.Context, string) (Caller, error)
	ResolvePublishedObjectType(ctx context.Context, operatorID, objectTypeRef string) (PublishedObjectType, error)
}

type OperatorIDResolver func(*http.Request) (string, bool)
type ManagementHandlerFactory func(ManagementServices, OperatorIDResolver) http.Handler

var (
	managementFactory    ManagementHandlerFactory
	managementMinEdition licverify.Edition
	managementFrozen     bool
)

func RegisterManagementHandler(min licverify.Edition, factory ManagementHandlerFactory) {
	if factory == nil {
		panic("rowfilter: RegisterManagementHandler(nil)")
	}
	if managementFrozen || managementFactory != nil {
		panic("rowfilter: management handler already registered or mounted")
	}
	if load() == nil {
		panic("rowfilter: management handler requires Enterprise resolver")
	}
	if min != licverify.EditionEnterprise {
		panic("rowfilter: management handler requires Enterprise edition")
	}
	entitlement.MustBeAssembling("rowfilter management")
	managementFactory = factory
	managementMinEdition = min
}

// ManagementGate runs before authentication so a below-Enterprise binary is
// externally indistinguishable from an unmounted Community route.
func ManagementGate() gin.HandlerFunc {
	min := managementMinEdition
	if min == "" {
		return func(c *gin.Context) { c.Next() }
	}
	return func(c *gin.Context) {
		if entitlement.AtLeast(min) {
			c.Next()
			return
		}
		slog.Info("rowfilter: management route hidden, licence below Enterprise", "capability", Capability, "min_edition", string(min))
		c.Abort()
		c.Data(http.StatusNotFound, gin.MIMEPlain, []byte("404 page not found"))
	}
}

type managementOperatorContextKey struct{}

func operatorIDFromRequest(request *http.Request) (string, bool) {
	operatorID, ok := request.Context().Value(managementOperatorContextKey{}).(string)
	return operatorID, ok && operatorID != ""
}

// ManagementOperatorID reads the authenticated operator attached by
// MountManagement. Core-to-EE adapters use it to preserve the operator's
// identity when a management request must consult another Core service.
func ManagementOperatorID(ctx context.Context) (string, bool) {
	operatorID, ok := ctx.Value(managementOperatorContextKey{}).(string)
	return operatorID, ok && operatorID != ""
}

// MountManagement returns false until the Enterprise implementation and the
// published-model resolver are both assembled. This is a fail-closed release
// gate: a management write surface without field validation is unsafe.
func MountManagement(group *gin.RouterGroup, services ManagementServices, resolveOperator func(*gin.Context) (string, bool)) bool {
	managementFrozen = true
	if managementFactory == nil {
		return false
	}
	if group == nil || services == nil || resolveOperator == nil {
		panic("rowfilter: incomplete management route dependencies")
	}
	handler := managementFactory(services, operatorIDFromRequest)
	if handler == nil {
		panic("rowfilter: management handler factory returned nil")
	}
	serve := func(c *gin.Context) {
		request := c.Request
		if operatorID, ok := resolveOperator(c); ok {
			request = request.WithContext(context.WithValue(request.Context(), managementOperatorContextKey{}, operatorID))
		}
		handler.ServeHTTP(c.Writer, request)
	}
	group.GET("/row-filter-policies", serve)
	group.PATCH("/row-filter-policies", serve)
	group.POST("/row-filter-policies/explain", serve)
	return true
}

func resetManagement() {
	managementFactory = nil
	managementMinEdition = ""
	managementFrozen = false
}
