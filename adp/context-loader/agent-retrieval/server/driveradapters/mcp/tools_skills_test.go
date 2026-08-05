// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"slices"
	"testing"
)

// TestExecuteSkillOnlyAppearsWhenEnabled 盯住那条命令执行通道的默认状态。
//
// execute_skill 把模型生成的 shell 送进沙箱，默认必须既不在 tools/list 也不在
// /mcp/info——「未开启」要和「没编译进来」长得一样，否则等于对着任何探测者
// 广播这台服务能执行命令。
func TestExecuteSkillOnlyAppearsWhenEnabled(t *testing.T) {
	noExtensions(t)

	if slices.Contains(assembledNames(t), toolKeyExecuteSkill) {
		t.Fatalf("execute_skill 在未开启时出现在 tools/list")
	}
	info, err := BuildMCPInfo("https://example.invalid/mcp")
	if err != nil {
		t.Fatalf("BuildMCPInfo: %v", err)
	}
	for _, tool := range info.Tools {
		if tool.Name == toolKeyExecuteSkill {
			t.Fatalf("execute_skill 在未开启时出现在 /mcp/info")
		}
	}

	t.Setenv(executeSkillEnabledEnv, "true")

	if !slices.Contains(assembledNames(t), toolKeyExecuteSkill) {
		t.Fatalf("开启后 execute_skill 仍未出现在 tools/list")
	}
	info, err = BuildMCPInfo("https://example.invalid/mcp")
	if err != nil {
		t.Fatalf("BuildMCPInfo: %v", err)
	}
	found := false
	for _, tool := range info.Tools {
		if tool.Name == toolKeyExecuteSkill {
			found = true
		}
	}
	if !found {
		t.Fatalf("开启后 execute_skill 仍未出现在 /mcp/info")
	}
}

// TestSkillToolsAdvertiseSchemas 三条读工具的 schema 必须能加载且非空——
// 装配期 loadToolSchemas 是 panic 语义，漏文件会在服务启动时才炸。
func TestSkillToolsAdvertiseSchemas(t *testing.T) {
	noExtensions(t)

	for _, key := range []string{toolKeyListSkills, toolKeyGetSkillContent, toolKeyReadSkillFile, toolKeyExecuteSkill} {
		in, out := tryLoadToolSchemas(key)
		if len(in) == 0 {
			t.Fatalf("%s 缺 input_schema", key)
		}
		if len(out) == 0 {
			t.Fatalf("%s 缺 output_schema", key)
		}
	}
}
