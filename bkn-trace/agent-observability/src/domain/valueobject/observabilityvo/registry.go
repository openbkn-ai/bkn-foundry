// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package observabilityvo

import "sort"

// Public log event allowlist derived from the canonical OpenBKN Audit registry.
// The auditvalidator cross-contract test requires every embedded registry event
// to remain visible here; do not add extension events without governing them.
var registeredEventCategories = map[string]string{
	"login.succeeded":                               CategoryAccessUser,
	"login.failed":                                  CategoryAccessUser,
	"logout.succeeded":                              CategoryAccessUser,
	"authorization.decided":                         CategoryAuditSecurity,
	"safe.admin.operation.observed":                 CategoryAuditAdmin,
	"conversation.created":                          CategoryRuntimeBusiness,
	"operation.executed":                            CategoryRuntimeBusiness,
	"operation.completed":                           CategoryRuntimeBusiness,
	"operation.failed":                              CategoryRuntimeBusiness,
	"log.query.authorized":                          CategoryAuditSecurity,
	"log.query.denied":                              CategoryAuditSecurity,
	"backend.operation.observed":                    CategoryAuditAdmin,
	"vega.operation.observed":                       CategoryAuditAdmin,
	"model_manager.operation.observed":              CategoryAuditAdmin,
	"execution_factory.operation.observed":          CategoryAuditAdmin,
	"trace_evidence.configuration_change_requested": CategoryAuditAdmin,
	"trace_evidence.operation_succeeded":            CategoryAuditAdmin,
	"trace_evidence.operation_failed":               CategoryAuditAdmin,
	"trace_evidence.rollback_completed":             CategoryAuditAdmin,
}

func IsRegisteredLogEvent(category, eventName string) bool {
	registeredCategory, ok := registeredEventCategories[eventName]
	return ok && registeredCategory == category
}

func IsRegisteredEventName(eventName string) bool {
	_, ok := registeredEventCategories[eventName]
	return ok
}

func RegisteredEventNames(categories []string) []string {
	allowed := make(map[string]struct{}, len(categories))
	for _, category := range categories {
		allowed[category] = struct{}{}
	}
	result := make([]string, 0)
	for eventName, category := range registeredEventCategories {
		if _, ok := allowed[category]; ok {
			result = append(result, eventName)
		}
	}
	sort.Strings(result)
	return result
}
