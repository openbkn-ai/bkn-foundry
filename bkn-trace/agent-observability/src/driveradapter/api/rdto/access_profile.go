package rdto

type AccessProfileResponse struct {
	BusinessProvenanceOwn             bool   `json:"business_provenance_own"`
	BusinessProvenanceManagedNetworks bool   `json:"business_provenance_managed_networks"`
	TechnicalTrace                    bool   `json:"technical_trace"`
	SecurityAudit                     bool   `json:"security_audit"`
	ManagementAudit                   bool   `json:"management_audit"`
	GlobalLogSearch                   bool   `json:"global_log_search"`
	AccessScopeFingerprint            string `json:"access_scope_fingerprint"`
}
