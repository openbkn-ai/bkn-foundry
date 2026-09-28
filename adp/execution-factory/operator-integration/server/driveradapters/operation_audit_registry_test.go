package driveradapters

import (
	"net/http"
	"testing"
)

func TestRegisteredOperationAuditExcludesRuntimeEndpoints(t *testing.T) {
	for _, endpoint := range []string{"/operator/debug", "/operator/proxy/:operator_id", "/tool-box/:box_id/proxy/:tool_id", "/mcp/:mcp_id/tool/:tool_name/debug", "/skills/:skill_id/execute", "/skills/index/build"} {
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

func TestRegisteredOperationAuditCoversConvertOperatorToTool(t *testing.T) {
	rule, ok := registeredOperationAudit(http.MethodPost, "/operator/convert/tool")
	if !ok || rule.Action != "create" || rule.TargetType != "tool" {
		t.Fatalf("convert-to-tool audit rule = %+v, %v", rule, ok)
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
