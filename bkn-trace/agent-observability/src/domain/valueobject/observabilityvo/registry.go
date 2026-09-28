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
	"login.succeeded":                      CategoryAccessUser,
	"login.failed":                         CategoryAccessUser,
	"logout.succeeded":                     CategoryAccessUser,
	"token.exchanged":                      CategoryAccessUser,
	"resource.read":                        CategoryAccessUser,
	"authorization.decided":                CategoryAuditSecurity,
	"access.denied":                        CategoryAuditSecurity,
	"access.anomaly_detected":              CategoryAuditSecurity,
	"source.identity_spoofed":              CategoryAuditSecurity,
	"safe.admin.operation.observed":        CategoryAuditAdmin,
	"user.created":                         CategoryAuditAdmin,
	"role.updated":                         CategoryAuditAdmin,
	"model_config.changed":                 CategoryAuditAdmin,
	"resource_config.changed":              CategoryAuditAdmin,
	"model.inference.completed":            CategoryRuntimeModel,
	"model.inference.failed":               CategoryRuntimeModel,
	"model.embedding.completed":            CategoryRuntimeModel,
	"knowledge.read.completed":             CategoryRuntimeBusiness,
	"logic.execution.completed":            CategoryRuntimeBusiness,
	"action.executed":                      CategoryRuntimeBusiness,
	"service.started":                      CategoryRuntimeSystem,
	"dependency.failed":                    CategoryRuntimeSystem,
	"conversation.created":                 CategoryRuntimeBusiness,
	"operation.executed":                   CategoryRuntimeBusiness,
	"conversation.closed":                  CategoryRuntimeBusiness,
	"conversation.expired":                 CategoryRuntimeBusiness,
	"interaction.started":                  CategoryRuntimeBusiness,
	"interaction.completed":                CategoryRuntimeBusiness,
	"interaction.failed":                   CategoryRuntimeBusiness,
	"interaction.canceled":                 CategoryRuntimeBusiness,
	"interaction.handed_off":               CategoryRuntimeBusiness,
	"interaction.abandoned":                CategoryRuntimeBusiness,
	"operation.started":                    CategoryRuntimeBusiness,
	"operation.completed":                  CategoryRuntimeBusiness,
	"operation.failed":                     CategoryRuntimeBusiness,
	"log.query.authorized":                 CategoryAuditSecurity,
	"log.query.denied":                     CategoryAuditSecurity,
	"log.export.requested":                 CategoryAuditSecurity,
	"log.export.completed":                 CategoryAuditSecurity,
	"log.record.quarantined":               CategoryAuditSecurity,
	"collection.records_dropped":           CategoryRuntimeSystem,
	"agent.config.changed":                 CategoryAuditAdmin,
	"tool.config.changed":                  CategoryAuditAdmin,
	"skill.config.changed":                 CategoryAuditAdmin,
	"toolbox.config.changed":               CategoryAuditAdmin,
	"mcp.config.changed":                   CategoryAuditAdmin,
	"audit_retention_policy.changed":       CategoryAuditAdmin,
	"backend.operation.observed":           CategoryAuditAdmin,
	"vega.operation.observed":              CategoryAuditAdmin,
	"model_manager.operation.observed":     CategoryAuditAdmin,
	"execution_factory.operation.observed": CategoryAuditAdmin,
	"agent.executed":                       CategoryRuntimeBusiness,
	"sandbox.session.changed":              CategoryRuntimeSystem,
	"sandbox.execution.completed":          CategoryRuntimeSystem,
	"sandbox.dependency.changed":           CategoryRuntimeSystem,
	"sandbox.policy.denied":                CategoryAuditSecurity,
	"secret.detected":                      CategoryAuditSecurity,
	// The four control-plane Audit events are frozen in the current Audit v1 registry.
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
