// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import (
	"fmt"
	"strings"
)

// Audit ledger snapshots use the stable business language, independent of the reader locale.
func AuditSummary(action, target, name string) string {
	actions := map[string]string{"create": "新建", "update": "修改", "delete": "删除", "import": "导入", "add_members": "添加成员", "remove_members": "移除成员", "enable": "启用", "disable": "停用", "start": "启动", "stop": "停止", "attach": "绑定", "detach": "解绑"}
	targets := map[string]string{"knowledge_network": "领域知识网络", "object_type": "对象类", "relation_type": "关系类", "action_type": "行动类", "risk_type": "风险类", "metric": "指标", "logic_property": "逻辑属性", "concept_group": "概念分组", "action_schedule": "行动计划", "kn_capability_binding": "知识网络能力绑定", "catalog": "数据目录", "resource": "数据资源", "discovery_schedule": "发现计划", "build_task": "构建任务", "connector_type": "连接器", "discovery_task": "发现任务", "semantic_understanding_task": "语义理解任务"}
	return actions[action] + targets[target] + "“" + name + "”"
}

func AuditNames(names []string) string {
	if len(names) == 1 {
		return names[0]
	}
	shown := make([]string, 0, 5)
	for _, name := range names {
		if len(shown) == 5 || len(strings.Join(shown, "、"))+len(name) > 400 {
			break
		}
		shown = append(shown, name)
	}
	if len(shown) == 0 {
		return ""
	}
	if len(shown) < len(names) {
		return fmt.Sprintf("%s等，共%d个（名称摘要已截断）", strings.Join(shown, "、"), len(names))
	}
	return fmt.Sprintf("%s，共%d个", strings.Join(shown, "、"), len(names))
}
