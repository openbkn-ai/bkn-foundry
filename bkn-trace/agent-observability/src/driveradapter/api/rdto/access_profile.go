// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package rdto

type AccessProfileResponse struct {
	BusinessProvenanceOwn             bool     `json:"business_provenance_own"`
	BusinessProvenanceManagedNetworks bool     `json:"business_provenance_managed_networks"`
	TechnicalTrace                    bool     `json:"technical_trace"`
	SecurityAudit                     bool     `json:"security_audit"`
	ManagementAudit                   bool     `json:"management_audit"`
	GlobalLogSearch                   bool     `json:"global_log_search"`
	AllowedLogCategories              []string `json:"allowed_log_categories"`
	LogSensitiveFields                bool     `json:"log_sensitive_fields"`
	LogExport                         bool     `json:"log_export"`
	LogPolicyRead                     bool     `json:"log_policy_read"`
	ObservabilityArchiveManage        bool     `json:"observability_archive_manage"`
	AccessScopeFingerprint            string   `json:"access_scope_fingerprint"`
}
