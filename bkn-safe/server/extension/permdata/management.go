// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package permdata

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/licverify"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/permissionproposal"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/bkn-foundry/comm-go/propertyaccess"
)

// UserGrantAuthority distinguishes unrestricted platform authorization
// administrators from callers delegated through authorize on one object type.
type UserGrantAuthority struct {
	Allowed      bool
	Unrestricted bool
}

// ManagementServices is the core-owned authorization surface used by the
// Enterprise property-grant manager. Implementations must return effective,
// base-clamped property levels; returning sparse extension levels directly
// would let a delegated caller grant more than they hold.
type ManagementServices interface {
	AuthorizeUserGrants(ctx context.Context, operatorID, objectTypeRef string) (UserGrantAuthority, error)
	AuthorizeRoleGrants(ctx context.Context, operatorID, objectTypeRef string) (UserGrantAuthority, error)
	GrantSubjectExists(ctx context.Context, subjectType, subjectID string) (bool, error)
	EffectivePropertyLevels(ctx context.Context, operatorID, objectTypeRef string, propertyNames []string) (map[string]propertyaccess.Level, error)
}

// OperatorIDResolver reads the authenticated operator attached by MountManagement.
// The identity is never accepted from query or request-body data.
type OperatorIDResolver func(*http.Request) (string, bool)

// ManagementAuditResult is the committed business identity reported by the EE
// handler. Core resolves display snapshots from its authoritative directories;
// EE never supplies caller-controlled names.
type ManagementAuditResult struct {
	Action        string
	SubjectType   string
	SubjectID     string
	ObjectTypeRef string
}

type ManagementAuditReporter func(*http.Request, ManagementAuditResult)

// ManagementHandlerFactory builds the private Enterprise business handler.
// Core owns the public path, methods, entitlement gate, authentication and
// account-state middleware; the factory owns only management behavior.
type ManagementHandlerFactory func(services ManagementServices, operatorID OperatorIDResolver, audit ManagementAuditReporter) http.Handler
type PermissionProposalHandlerFactory func(services ManagementServices) permissionproposal.Handler

var (
	managementFactory    ManagementHandlerFactory
	managementMinEdition licverify.Edition
	managementFrozen     bool
	proposalFactory      PermissionProposalHandlerFactory
)

func RegisterPermissionProposalHandler(factory PermissionProposalHandlerFactory) {
	if factory == nil || proposalFactory != nil {
		panic("permdata: invalid permission proposal handler registration")
	}
	proposalFactory = factory
}

// RegisterManagementHandler installs the Enterprise management handler. The
// property resolver must be registered first and both surfaces must declare the
// same minimum edition so one capability cannot be only partially available.
func RegisterManagementHandler(min licverify.Edition, factory ManagementHandlerFactory) {
	if factory == nil {
		panic("permdata: RegisterManagementHandler(nil)")
	}
	if managementFrozen {
		panic("permdata: RegisterManagementHandler after management routes were mounted")
	}
	if managementFactory != nil {
		panic("permdata: management handler already registered")
	}
	if load() == nil || minEdition == "" {
		panic("permdata: property resolver must be registered before the management handler")
	}
	if min != minEdition {
		panic("permdata: management handler and resolver must use the same minimum edition")
	}
	entitlement.MustBeAssembling("permdata management")
	managementFactory = factory
	managementMinEdition = min
}

// ManagementGate hides the registered management path when the live licence
// is below Enterprise. Its response intentionally matches gin's unmounted
// route byte for byte so an unauthenticated probe cannot distinguish an
// unlicensed Enterprise binary from Community.
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
		slog.Info("permdata: management route hidden, licence below the required tier",
			"capability", Capability,
			"min_edition", string(min),
			"edition", string(entitlement.Current()),
			"method", c.Request.Method,
			"path", c.Request.URL.Path)
		c.Abort()
		c.Data(http.StatusNotFound, gin.MIMEPlain, []byte("404 page not found"))
	}
}

type operatorContextKey struct{}
type managementAuditContextKey struct{}

type managementAuditHolder struct {
	result *ManagementAuditResult
}

func operatorIDFromRequest(request *http.Request) (string, bool) {
	operatorID, ok := request.Context().Value(operatorContextKey{}).(string)
	return operatorID, ok && operatorID != ""
}

func reportManagementAudit(request *http.Request, result ManagementAuditResult) {
	if request == nil {
		return
	}
	if holder, ok := request.Context().Value(managementAuditContextKey{}).(*managementAuditHolder); ok && holder != nil {
		copy := result
		holder.result = &copy
	}
}

// MountManagement mounts the core-owned management path when an Enterprise
// handler was registered. resolveOperator bridges the authenticated gin
// identity into the standard-library request received by the EE handler.
func MountManagement(
	group *gin.RouterGroup,
	services ManagementServices,
	resolveOperator func(*gin.Context) (string, bool),
	consumeAudit func(*gin.Context, ManagementAuditResult),
) bool {
	managementFrozen = true
	if managementFactory == nil {
		return false
	}
	if group == nil || services == nil || resolveOperator == nil || consumeAudit == nil {
		panic("permdata: incomplete management route dependencies")
	}
	handler := managementFactory(services, operatorIDFromRequest, reportManagementAudit)
	if handler == nil {
		panic("permdata: management handler factory returned nil")
	}
	if proposalFactory != nil {
		permissionproposal.Register("property_grants", proposalFactory(services))
	}
	serve := func(c *gin.Context) {
		request := c.Request
		holder := &managementAuditHolder{}
		request = request.WithContext(context.WithValue(request.Context(), managementAuditContextKey{}, holder))
		if operatorID, ok := resolveOperator(c); ok {
			request = request.WithContext(context.WithValue(request.Context(), operatorContextKey{}, operatorID))
		}
		handler.ServeHTTP(c.Writer, request)
		if holder.result != nil {
			consumeAudit(c, *holder.result)
		}
	}
	group.GET("/property-grants", serve)
	group.PATCH("/property-grants", serve)
	return true
}

func resetManagement() {
	managementFactory = nil
	managementMinEdition = ""
	managementFrozen = false
}
