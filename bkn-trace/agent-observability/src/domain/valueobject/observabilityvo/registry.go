package observabilityvo

import "sort"

// Code generated from OpenBKN 日志事件注册表.json OpenBKN-0.1.4-R1.
// Source SHA-256: 695ac993967e3a9ed49dd638d8a014fa34e1ee3d7d3535b163793ad224e12f10.
// Do not add extension events here without updating the governed registry first.
var registeredEventCategories = map[string]string{
	"login.succeeded":             CategoryAccessUser,
	"login.failed":                CategoryAccessUser,
	"token.exchanged":             CategoryAccessUser,
	"resource.read":               CategoryAccessUser,
	"authorization.decided":       CategoryAuditSecurity,
	"access.denied":               CategoryAuditSecurity,
	"access.anomaly_detected":     CategoryAuditSecurity,
	"source.identity_spoofed":     CategoryAuditSecurity,
	"user.created":                CategoryAuditAdmin,
	"role.updated":                CategoryAuditAdmin,
	"model_config.changed":        CategoryAuditAdmin,
	"resource_config.changed":     CategoryAuditAdmin,
	"model.inference.completed":   CategoryRuntimeModel,
	"model.inference.failed":      CategoryRuntimeModel,
	"model.embedding.completed":   CategoryRuntimeModel,
	"knowledge.read.completed":    CategoryRuntimeBusiness,
	"logic.execution.completed":   CategoryRuntimeBusiness,
	"action.executed":             CategoryRuntimeBusiness,
	"service.started":             CategoryRuntimeSystem,
	"dependency.failed":           CategoryRuntimeSystem,
	"conversation.created":        CategoryRuntimeBusiness,
	"conversation.closed":         CategoryRuntimeBusiness,
	"conversation.expired":        CategoryRuntimeBusiness,
	"interaction.started":         CategoryRuntimeBusiness,
	"interaction.completed":       CategoryRuntimeBusiness,
	"interaction.failed":          CategoryRuntimeBusiness,
	"interaction.canceled":        CategoryRuntimeBusiness,
	"interaction.handed_off":      CategoryRuntimeBusiness,
	"interaction.abandoned":       CategoryRuntimeBusiness,
	"operation.started":           CategoryRuntimeBusiness,
	"operation.completed":         CategoryRuntimeBusiness,
	"operation.failed":            CategoryRuntimeBusiness,
	"log.query.authorized":        CategoryAuditSecurity,
	"log.query.denied":            CategoryAuditSecurity,
	"log.export.requested":        CategoryAuditSecurity,
	"log.export.completed":        CategoryAuditSecurity,
	"log.record.quarantined":      CategoryAuditSecurity,
	"collection.records_dropped":  CategoryRuntimeSystem,
	"agent.config.changed":        CategoryAuditAdmin,
	"tool.config.changed":         CategoryAuditAdmin,
	"skill.config.changed":        CategoryAuditAdmin,
	"toolbox.config.changed":      CategoryAuditAdmin,
	"mcp.config.changed":          CategoryAuditAdmin,
	"agent.executed":              CategoryRuntimeBusiness,
	"sandbox.session.changed":     CategoryRuntimeSystem,
	"sandbox.execution.completed": CategoryRuntimeSystem,
	"sandbox.dependency.changed":  CategoryRuntimeSystem,
	"sandbox.policy.denied":       CategoryAuditSecurity,
	"secret.detected":             CategoryAuditSecurity,
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
