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

// TestExecuteSkillRouteFollowsTheSwitch 盯住那道总闸在 REST 这一侧也成立。
//
// 开关最初只管 MCP 工具面，REST 路由照常注册——于是文档里「唯一的命令执行通道」
// 在 REST 这侧是假的，而看文档决定要不要开的人会据此误判风险。关闭时这条路由
// 必须根本不存在，而不是存在后再拒绝。
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

// TestExecuteEnabledAcceptsLegacyEnvName 旧名是 MCP-only 时期留下的，
// 已经有人按旧名配过；升上来时不能把开着的能力悄悄关掉。
func TestExecuteEnabledAcceptsLegacyEnvName(t *testing.T) {
	t.Setenv(knskills.ExecuteEnabledEnv, "")
	t.Setenv("MCP_EXECUTE_SKILL_ENABLED", "true")
	if !knskills.ExecuteEnabled() {
		t.Fatal("旧环境变量名未被认，升级会把开着的能力关掉")
	}
}
