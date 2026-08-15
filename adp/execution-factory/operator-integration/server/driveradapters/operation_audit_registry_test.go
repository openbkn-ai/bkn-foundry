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
