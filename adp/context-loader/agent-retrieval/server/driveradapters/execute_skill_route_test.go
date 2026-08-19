// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knskills"
)

// TestExecuteSkillRouteFollowsTheSwitch is also true on the REST side.
//
// The switch initially only takes care of the MCP tool surface, and the REST route is still registered - so the "only command execution channel" in the document.
// It is false on the REST side, and people who look at the documentation to decide whether to open it will misjudge the risks based on this. This route is closed when.
// It must not exist at all, rather than exist and then be rejected.
func TestExecuteSkillRouteFollowsTheSwitch(t *testing.T) {
	hasRoute := func(t *testing.T) bool {
		t.Helper()
		gin.SetMode(gin.TestMode)
		engine := gin.New()
		group := engine.Group("/api/agent-retrieval/in/v1")
		if knskills.ExecuteEnabled() {
			group.POST("/kn/execute_skill", func(c *gin.Context) { c.Status(http.StatusOK) })
		}
		for _, route := range engine.Routes() {
			if route.Path == "/api/agent-retrieval/in/v1/kn/execute_skill" {
				return true
			}
		}
		return false
	}

	t.Setenv(knskills.ExecuteEnabledEnv, "")
	if hasRoute(t) {
		t.Fatal("开关关闭时 execute_skill 路由仍被注册")
	}

	t.Setenv(knskills.ExecuteEnabledEnv, "true")
	if !hasRoute(t) {
		t.Fatal("开关开启后 execute_skill 路由未注册")
	}
}

// The old name of TestExecuteEnabledAcceptsLegacyEnvName is left over from the MCP-only era.
// Someone has already assigned it under the old name; you cannot quietly turn off the ability that is on when you ascend.
func TestExecuteEnabledAcceptsLegacyEnvName(t *testing.T) {
	t.Setenv(knskills.ExecuteEnabledEnv, "")
	t.Setenv("MCP_EXECUTE_SKILL_ENABLED", "true")
	if !knskills.ExecuteEnabled() {
		t.Fatal("旧环境变量名未被认，升级会把开着的能力关掉")
	}
}
