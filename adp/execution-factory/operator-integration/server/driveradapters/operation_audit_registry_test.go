package driveradapters

import (
	"net/http"
	"testing"
)

func TestRegisteredOperationAuditExcludesRuntimeEndpoints(t *testing.T) {
	for _, endpoint := range []string{"/tool-box/:box_id/tool/:tool_id/debug", "/tool-box/:box_id/proxy/:tool_id", "/mcp/:mcp_id/tool/:tool_name/debug", "/skills/:skill_id/execute", "/skills/index/build"} {
		if _, ok := registeredOperationAudit(http.MethodPost, endpoint); ok {
			t.Fatalf("runtime endpoint %s must remain trace-only", endpoint)
		}
	}
}

func TestRegisteredOperationAuditCoversSkillLifecycle(t *testing.T) {
	if rule, ok := registeredOperationAudit(http.MethodPut, "/skills/:skill_id/status"); !ok || rule.TargetType != "skill" || rule.Action != "status_change" {
		t.Fatalf("skill status audit rule = %+v, %v", rule, ok)
	}
}

// The retired operator routes must not keep management audit rules.
func TestRegisteredOperationAuditHasNoOperatorRoutes(t *testing.T) {
	for _, test := range []struct{ method, path string }{
		{http.MethodPost, "/operator/register"},
		{http.MethodPost, "/operator/convert/tool"},
		{http.MethodPost, "/operator/category"},
	} {
		if _, ok := registeredOperationAudit(test.method, test.path); ok {
			t.Fatalf("retired route %s %s still has a public audit rule", test.method, test.path)
		}
		if _, ok := registeredPrivateOperationAudit(test.method, test.path); ok {
			t.Fatalf("retired route %s %s still has a private audit rule", test.method, test.path)
		}
	}
}

func TestRegisteredOperationAuditCoversCompoundManagementRequests(t *testing.T) {
	for _, test := range []struct{ path, action, targetType string }{
		{"/impex/import/:type", "import", "import_batch"},
		{"/capabilities/openapi-bundle", "create", "capability_bundle"},
	} {
		rule, ok := registeredOperationAudit(http.MethodPost, test.path)
		if !ok || rule.Action != test.action || rule.TargetType != test.targetType {
			t.Fatalf("compound route %s = %+v, %v", test.path, rule, ok)
		}
	}
}
