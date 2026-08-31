// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

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
	ActorID                    string
	EffectiveSubjectID         string
	ApplicationPrincipalID     string
	DelegationID               string
	Roles                      []string
	ManagedKnowledgeNetworkIDs []string
	AccountActive              bool
	Fingerprint                string
}

// RecordScope is the immutable access projection attached to a persisted run.
type RecordScope struct {
	EffectiveSubjectID     string
	ApplicationPrincipalID string
	KnowledgeNetworkIDs    []string
}

// CanReadRecord applies the same record-level decision to every read surface.
func CanReadRecord(profile AccessProfile, record RecordScope, view AccessView) bool {
	if !profile.AccountActive {
		return false
	}

	switch view {
	case AccessViewBusiness:
		return HasGlobalTraceAccess(profile) ||
			validRecordBoundary(record) && (ownsRecord(profile, record) || managesRecordNetwork(profile, record))
	case AccessViewTechnical:
		return HasGlobalTraceAccess(profile) ||
			validRecordBoundary(record) && (ownsRecord(profile, record) || managesRecordNetwork(profile, record))
	case AccessViewSecurity:
		return hasAnyRole(profile, "security", "super_admin")
	case AccessViewAudit:
		return hasAnyRole(profile, "audit", "super_admin")
	default:
		return false
	}
}

// HasGlobalTraceAccess preserves the existing administrator bypass for a complete
// Trace record after removing the tenant boundary.
func HasGlobalTraceAccess(profile AccessProfile) bool {
	return profile.AccountActive && hasAnyRole(profile, "admin", "super_admin")
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
		return HasGlobalTraceAccess(profile) ||
			hasAnyRole(profile, "network_builder") && len(profile.ManagedKnowledgeNetworkIDs) > 0
	case AccessViewTechnical:
		return HasGlobalTraceAccess(profile)
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

func validRecordBoundary(record RecordScope) bool {
	return record.EffectiveSubjectID != "" || record.ApplicationPrincipalID != ""
}

func ownsRecord(profile AccessProfile, record RecordScope) bool {
	if profile.EffectiveSubjectID != "" && record.EffectiveSubjectID != "" &&
		profile.EffectiveSubjectID == record.EffectiveSubjectID {
		return true
	}
	return profile.ApplicationPrincipalID != "" && record.ApplicationPrincipalID != "" &&
		profile.ApplicationPrincipalID == record.ApplicationPrincipalID
}

func managesRecordNetwork(profile AccessProfile, record RecordScope) bool {
	if !hasAnyRole(profile, "network_builder") || len(record.KnowledgeNetworkIDs) == 0 {
		return false
	}
	managed := make(map[string]struct{}, len(profile.ManagedKnowledgeNetworkIDs))
	for _, networkID := range profile.ManagedKnowledgeNetworkIDs {
		if networkID != "" {
			managed[networkID] = struct{}{}
		}
	}
	for _, networkID := range record.KnowledgeNetworkIDs {
		if _, ok := managed[networkID]; ok {
			return true
		}
	}
	return false
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
