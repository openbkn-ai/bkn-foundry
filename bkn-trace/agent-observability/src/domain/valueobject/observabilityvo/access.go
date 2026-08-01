package observabilityvo

import "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"

const (
	CategoryAccessUser      = "access.user"
	CategoryAuditAdmin      = "audit.admin"
	CategoryAuditSecurity   = "audit.security"
	CategoryRuntimeSystem   = "runtime.system"
	CategoryRuntimeBusiness = "runtime.business"
	CategoryRuntimeModel    = "runtime.model"
)

var AllCategories = []string{
	CategoryAccessUser,
	CategoryAuditAdmin,
	CategoryAuditSecurity,
	CategoryRuntimeSystem,
	CategoryRuntimeBusiness,
	CategoryRuntimeModel,
}

type AccessCapabilities struct {
	BusinessProvenanceOwn             bool
	BusinessProvenanceManagedNetworks bool
	TechnicalTrace                    bool
	SecurityAudit                     bool
	ManagementAudit                   bool
	GlobalLogSearch                   bool
	AllowedLogCategories              []string
	LogSensitiveFields                bool
	LogExport                         bool
	LogPolicyRead                     bool
}

func CapabilitiesFor(profile evidencevo.AccessProfile) AccessCapabilities {
	if !profile.AccountActive || !profile.TenantActive {
		return AccessCapabilities{AllowedLogCategories: []string{}}
	}

	hasRole := roleMatcher(profile.Roles)
	allowedCategories := make([]string, 0, len(AllCategories))
	appendCategories := func(categories ...string) {
		for _, category := range categories {
			if !contains(allowedCategories, category) {
				allowedCategories = append(allowedCategories, category)
			}
		}
	}
	if hasRole("network_builder", "admin", "super_admin") {
		appendCategories(CategoryRuntimeSystem, CategoryRuntimeBusiness, CategoryRuntimeModel)
	}
	if hasRole("security", "audit", "super_admin") {
		appendCategories(CategoryAccessUser)
	}
	if hasRole("audit", "super_admin") {
		appendCategories(CategoryAuditAdmin)
	}
	if hasRole("security", "audit", "super_admin") {
		appendCategories(CategoryAuditSecurity)
	}
	if hasRole("super_admin") {
		allowedCategories = append([]string(nil), AllCategories...)
	}

	globalLogSearch := hasRole("network_builder", "admin", "security", "audit", "super_admin")
	controlledLogAccess := hasRole("admin", "security", "audit", "super_admin")
	return AccessCapabilities{
		BusinessProvenanceOwn:             true,
		BusinessProvenanceManagedNetworks: hasRole("network_builder") && len(profile.ManagedKnowledgeNetworkIDs) > 0,
		TechnicalTrace:                    true,
		SecurityAudit:                     hasRole("security", "audit", "super_admin"),
		ManagementAudit:                   hasRole("audit", "super_admin"),
		GlobalLogSearch:                   globalLogSearch,
		AllowedLogCategories:              allowedCategories,
		LogSensitiveFields:                controlledLogAccess,
		LogExport:                         globalLogSearch,
		LogPolicyRead:                     controlledLogAccess,
	}
}

func roleMatcher(roles []string) func(...string) bool {
	roleSet := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		roleSet[role] = struct{}{}
	}
	return func(expected ...string) bool {
		for _, role := range expected {
			if _, ok := roleSet[role]; ok {
				return true
			}
		}
		return false
	}
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
