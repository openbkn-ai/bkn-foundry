package driveradapters

import "net/http"

type operationAuditRule struct{ Action, TargetType string }

// registeredOperationAudit is the deliberately small management allowlist.
// Execute, proxy, debug, parsing and index-running endpoints remain Trace facts.
func registeredOperationAudit(method, path string) (operationAuditRule, bool) {
	for _, item := range []struct {
		method, path string
		rule         operationAuditRule
	}{
		{http.MethodPost, "/operator/register", operationAuditRule{"create", "operator"}},
		{http.MethodDelete, "/operator/delete", operationAuditRule{"delete", "operator"}},
		{http.MethodPost, "/operator/status", operationAuditRule{"status_change", "operator"}},
		{http.MethodPost, "/operator/info", operationAuditRule{"update", "operator"}},
		{http.MethodPost, "/operator/info/update", operationAuditRule{"update", "operator"}},
		{http.MethodPost, "/tool-box", operationAuditRule{"create", "toolbox"}},
		{http.MethodPost, "/tool-box/:box_id", operationAuditRule{"update", "toolbox"}},
		{http.MethodDelete, "/tool-box/:box_id", operationAuditRule{"delete", "toolbox"}},
		{http.MethodPost, "/tool-box/:box_id/status", operationAuditRule{"status_change", "toolbox"}},
		{http.MethodPost, "/tool-box/:box_id/tool", operationAuditRule{"create", "tool"}},
		{http.MethodPost, "/tool-box/:box_id/tool/:tool_id", operationAuditRule{"update", "tool"}},
		{http.MethodPost, "/tool-box/:box_id/tools/batch-delete", operationAuditRule{"delete", "tool"}},
		{http.MethodPost, "/tool-box/:box_id/tools/status", operationAuditRule{"status_change", "tool"}},
		{http.MethodPost, "/operator/convert/tool", operationAuditRule{"create", "tool"}},
		{http.MethodPost, "/capabilities/openapi-bundle", operationAuditRule{"create", "capability_bundle"}},
		{http.MethodPost, "/impex/import/:type", operationAuditRule{"import", "import_batch"}},
		{http.MethodPost, "/mcp/", operationAuditRule{"create", "mcp"}},
		{http.MethodPut, "/mcp/:mcp_id", operationAuditRule{"update", "mcp"}},
		{http.MethodDelete, "/mcp/:mcp_id", operationAuditRule{"delete", "mcp"}},
		{http.MethodPost, "/mcp/:mcp_id/status", operationAuditRule{"status_change", "mcp"}},
		{http.MethodPost, "/skills", operationAuditRule{"create", "skill"}},
		{http.MethodDelete, "/skills/:skill_id", operationAuditRule{"delete", "skill"}},
		{http.MethodPut, "/skills/:skill_id/status", operationAuditRule{"status_change", "skill"}},
		{http.MethodPut, "/skills/:skill_id/metadata", operationAuditRule{"update", "skill"}},
		{http.MethodPut, "/skills/:skill_id/package", operationAuditRule{"update", "skill"}},
		{http.MethodPost, "/skills/:skill_id/history/republish", operationAuditRule{"publish", "skill"}},
		{http.MethodPost, "/skills/:skill_id/history/publish", operationAuditRule{"publish", "skill"}},
	} {
		if method == item.method && path == item.path {
			return item.rule, true
		}
	}
	return operationAuditRule{}, false
}

// Private management calls currently trust caller-supplied X-Account-ID for
// business authorization. Their Audit observes the request, but must never
// represent that claimed ID as an independently verified actor.
func registeredPrivateOperationAudit(method, path string) (operationAuditRule, bool) {
	switch {
	case method == http.MethodPost && path == "/operator/register":
		return operationAuditRule{"create", "operator"}, true
	case method == http.MethodPost && path == "/operator/info/update":
		return operationAuditRule{"update", "operator"}, true
	case method == http.MethodPost && path == "/operator/category":
		return operationAuditRule{"create", "operator_category"}, true
	case method == http.MethodPut && path == "/operator/category/:category_type":
		return operationAuditRule{"update", "operator_category"}, true
	case method == http.MethodDelete && path == "/operator/category/:category_type":
		return operationAuditRule{"delete", "operator_category"}, true
	default:
		return operationAuditRule{}, false
	}
}
