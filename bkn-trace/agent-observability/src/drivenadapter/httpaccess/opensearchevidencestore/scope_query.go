package opensearchevidencestore

import "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"

func scopeCandidateMust(scope evidencevo.QueryScope) []map[string]any {
	must := make([]map[string]any, 0, 3)
	for _, item := range []struct {
		field string
		value string
	}{
		{"bkn.tenant.id", scope.TenantID},
		{"business_domain", scope.BusinessDomain},
	} {
		if item.value != "" {
			must = append(must, map[string]any{"bool": exactTermQuery(item.field, item.value)})
		}
	}

	if scope.AccessProfile == nil {
		return appendLegacyOwnerMust(must, scope)
	}
	if scope.View != "" && scope.View != evidencevo.AccessViewBusiness {
		if evidencevo.NeedsCrossAccountCandidates(scope) {
			return must
		}
		return appendLegacyOwnerMust(must, scope)
	}

	profile := *scope.AccessProfile
	should := make([]map[string]any, 0, 4)
	if profile.EffectiveSubjectID != "" {
		should = append(should, map[string]any{"bool": exactTermQuery("effective_subject_id", profile.EffectiveSubjectID)})
	}
	if profile.ApplicationPrincipalID != "" {
		should = append(should, map[string]any{"bool": exactTermQuery("application_principal_id", profile.ApplicationPrincipalID)})
	}
	if scope.AccountID != "" {
		ownerMust := appendLegacyOwnerMust(nil, scope)
		should = append(should, map[string]any{"bool": map[string]any{"must": ownerMust}})
	}
	if evidencevo.NeedsCrossAccountCandidates(scope) {
		if evidencevo.HasManagedKnowledgeNetworkWildcard(profile) {
			should = append(should, map[string]any{"exists": map[string]any{
				"field": "knowledge_network_ids",
			}})
		} else {
			should = append(should, map[string]any{"terms": map[string]any{
				"knowledge_network_ids": profile.ManagedKnowledgeNetworkIDs,
			}})
		}
	}
	if len(should) == 0 {
		return appendLegacyOwnerMust(must, scope)
	}
	return append(must, map[string]any{"bool": map[string]any{
		"should": should, "minimum_should_match": 1,
	}})
}

func appendLegacyOwnerMust(must []map[string]any, scope evidencevo.QueryScope) []map[string]any {
	for _, item := range []struct {
		field string
		value string
	}{
		{"bkn.account.id", scope.AccountID},
		{"bkn.account.type", scope.AccountType},
	} {
		if item.value != "" {
			must = append(must, map[string]any{"bool": exactTermQuery(item.field, item.value)})
		}
	}
	return must
}
