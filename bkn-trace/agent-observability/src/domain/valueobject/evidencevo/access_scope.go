package evidencevo

// AccessView identifies the independently authorized projection requested by a caller.
type AccessView string

const (
	AccessViewBusiness  AccessView = "business"
	AccessViewTechnical AccessView = "technical"
	AccessViewSecurity  AccessView = "security"
	AccessViewAudit     AccessView = "audit"
)

// AccessProfile is derived from trusted gateway identity and current BKN Safe grants.
// Callers cannot supply roles or managed knowledge networks in request payloads.
type AccessProfile struct {
	TenantID                    string
	BusinessDomain              string
	ActorID                     string
	EffectiveSubjectID          string
	ApplicationPrincipalID      string
	DelegationID                string
	Roles                       []string
	ManagedKnowledgeNetworkIDs  []string
	ManagesAllKnowledgeNetworks bool
	AccountActive               bool
	TenantActive                bool
	Fingerprint                 string
}

// RecordScope is the immutable access projection attached to a persisted run.
type RecordScope struct {
	TenantID               string
	BusinessDomain         string
	EffectiveSubjectID     string
	ApplicationPrincipalID string
	KnowledgeNetworkIDs    []string
}

// CanReadRecord applies the same record-level decision to every read surface.
func CanReadRecord(profile AccessProfile, record RecordScope, view AccessView) bool {
	if !validAccessBoundary(profile, record) {
		return false
	}

	switch view {
	case AccessViewBusiness:
		return ownsRecord(profile, record) || managesEveryRecordNetwork(profile, record)
	case AccessViewTechnical:
		return hasAnyRole(profile, "admin", "super_admin")
	case AccessViewSecurity:
		return hasAnyRole(profile, "security", "super_admin")
	case AccessViewAudit:
		return hasAnyRole(profile, "audit", "super_admin")
	default:
		return false
	}
}

// NeedsCrossAccountCandidates allows stores to widen only the candidate query;
// CanReadRecord must still authorize every returned record.
func NeedsCrossAccountCandidates(scope QueryScope) bool {
	if scope.AccessProfile == nil {
		return false
	}
	profile := *scope.AccessProfile
	switch defaultAccessView(scope.View) {
	case AccessViewBusiness:
		return hasAnyRole(profile, "network_builder") &&
			(profile.ManagesAllKnowledgeNetworks || len(profile.ManagedKnowledgeNetworkIDs) > 0)
	case AccessViewTechnical:
		return hasAnyRole(profile, "admin", "super_admin")
	case AccessViewSecurity:
		return hasAnyRole(profile, "security", "super_admin")
	case AccessViewAudit:
		return hasAnyRole(profile, "audit", "super_admin")
	default:
		return false
	}
}

func defaultAccessView(view AccessView) AccessView {
	if view == "" {
		return AccessViewBusiness
	}
	return view
}

func validAccessBoundary(profile AccessProfile, record RecordScope) bool {
	if !profile.AccountActive || !profile.TenantActive ||
		profile.TenantID == "" || record.TenantID == "" ||
		profile.TenantID != record.TenantID {
		return false
	}
	if record.BusinessDomain != "" && profile.BusinessDomain != record.BusinessDomain {
		return false
	}
	return true
}

func ownsRecord(profile AccessProfile, record RecordScope) bool {
	if profile.EffectiveSubjectID != "" && record.EffectiveSubjectID != "" &&
		profile.EffectiveSubjectID == record.EffectiveSubjectID {
		return true
	}
	return profile.ApplicationPrincipalID != "" && record.ApplicationPrincipalID != "" &&
		profile.ApplicationPrincipalID == record.ApplicationPrincipalID
}

func managesEveryRecordNetwork(profile AccessProfile, record RecordScope) bool {
	if !hasAnyRole(profile, "network_builder") || len(record.KnowledgeNetworkIDs) == 0 {
		return false
	}
	for _, networkID := range record.KnowledgeNetworkIDs {
		if networkID == "" {
			return false
		}
	}
	if profile.ManagesAllKnowledgeNetworks {
		return true
	}
	managed := make(map[string]struct{}, len(profile.ManagedKnowledgeNetworkIDs))
	for _, networkID := range profile.ManagedKnowledgeNetworkIDs {
		if networkID != "" {
			managed[networkID] = struct{}{}
		}
	}
	for _, networkID := range record.KnowledgeNetworkIDs {
		if _, ok := managed[networkID]; !ok {
			return false
		}
	}
	return true
}

func hasAnyRole(profile AccessProfile, expected ...string) bool {
	roles := make(map[string]struct{}, len(profile.Roles))
	for _, role := range profile.Roles {
		roles[role] = struct{}{}
	}
	for _, role := range expected {
		if _, ok := roles[role]; ok {
			return true
		}
	}
	return false
}
