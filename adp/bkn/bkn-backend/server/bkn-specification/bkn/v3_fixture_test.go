// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// v3Files is a 3.0 package written in the one spelling 3.0 accepts. Tests change one file at a
// time to hit one rule.
var v3Files = map[string]string{
	"network.bkn": `---
type: knowledge_network
id: kn
name: kn
tags: []
bkn_spec: "3.0"
---

# kn

运维网络。
`,
	"object_types/pod.bkn": "---\ntype: object_type\nid: pod\nname: Pod\ntags: [k8s]\n---\n\n## ObjectType: Pod\n\nPod 描述。\n\n" +
		"### Data Source\n\n| Type | ID | Name |\n|------|----|------|\n| resource | res_pod | pods |\n\n" +
		"### Data Properties\n\n" +
		"| Name | Display Name | Type | Semantic | Required | Description | Mapped Field |\n" +
		"|------|--------------|------|----------|----------|-------------|--------------|\n" +
		"| id | ID | string | | | | id |\n" +
		"| pod_name | 名称 | string | | true | | pod_name |\n" +
		"| created_at | 创建时间 | datetime | event_time | | | created_at |\n" +
		"| cpu_pct | CPU | float | | | | cpu_pct |\n\n" +
		"### Property Constraints\n\n```yaml\ncpu_pct:\n  range: {min: 0, max: 100}\n  unit: \"%\"\ncreated_at:\n  timezone: Asia/Shanghai\n```\n\n" +
		"### Logic Properties\n\n#### cpu\n\n**Meta**\n\n| Display Name | Type | Description |\n|--------------|------|-------------|\n| CPU | metric | |\n\n" +
		"**Source**\n\n| Source Type | Source ID | Source Name |\n|-------------|-----------|-------------|\n| metric | m_cpu | |\n\n" +
		"**Parameters**\n\n| Name | Type | Source | Operation | ValueFrom | Value | Description |\n|------|------|--------|-----------|-----------|-------|-------------|\n| pod | string | | == | property | pod_name | |\n\n" +
		"### Keys\n\nPrimary Keys: id\nDisplay Key: pod_name\nIncremental Key: created_at\n",
	"relation_types/pod_peer.bkn": "---\ntype: relation_type\nid: pod_peer\nname: 同组\ntags: []\n---\n\n## RelationType: 同组\n\n" +
		"### Endpoint\n\n| Source | Target | Type |\n|--------|--------|------|\n| pod | pod | direct |\n\n" +
		"### Mapping Rules\n\n| Source Property | Target Property |\n|-----------------|-----------------|\n| pod_name | pod_name |\n",
	"action_types/restart_pod.bkn": "---\ntype: action_type\nid: restart_pod\nname: 重启Pod\ntags: []\naction_intent: modify\n---\n\n## ActionType: 重启Pod\n\n重启异常 Pod。\n\n" +
		"### Bound Object\n\n| Bound Object |\n|--------------|\n| pod |\n\n" +
		"### Impact Contracts\n\n```yaml\nimpact_contracts:\n  - object_type_id: pod\n    expected_operation: modify\n```\n\n" +
		"### Trigger Condition\n\n```yaml\nfield: pod_name\noperation: not_empty\n```\n\n" +
		"### Action Source\n\n| Type | BoxID | ToolID | McpID | ToolName |\n|------|-------|--------|-------|----------|\n| tool | box_k8s | tool_restart | | |\n\n" +
		"### Parameter Binding\n\n| Name | Type | Source | Operation | ValueFrom | Value | Description |\n|------|------|--------|-----------|-----------|-------|-------------|\n" +
		"| pod | string | Body | | property | pod_name | Pod 名称 |\n| reason | string | Body | | input | | 原因 |\n\n" +
		"### Schedule\n\n| Type | Expression |\n|------|------------|\n\n",
	"risk_types/restart_risk.bkn": "---\ntype: risk_type\nid: restart_risk\nname: 重启风险\ntags: []\n---\n\n## RiskType: 重启风险\n\n重启会中断服务。\n\n### 控制策略\n\n维护窗口内执行。\n",
	"concept_groups/k8s.bkn": "---\ntype: concept_group\nid: k8s\nname: K8s\ntags: []\n---\n\n## ConceptGroup: K8s\n\n" +
		"### Object Types\n\n| ID | Name | Description |\n|----|------|-------------|\n| pod | Pod | Pod 描述。 |\n",
}

// loadV3 loads v3Files with edits applied. An edit replaces one exact substring of a file, with an
// empty old adds the file, and with an empty old and new removes it; it fails the test when the
// substring is not there, so a fixture change cannot silently turn a refusal case into a pass.
func loadV3(t *testing.T, edits ...v3Edit) (*BknNetwork, error) {
	t.Helper()
	files := make(map[string]string, len(v3Files))
	for path, text := range v3Files {
		files[path] = text
	}
	for _, e := range edits {
		if e.old == "" && e.new == "" {
			delete(files, e.file)
			continue
		}
		if e.old == "" {
			files[e.file] = e.new
			continue
		}
		text, ok := files[e.file]
		require.True(t, ok, "fixture has no %s", e.file)
		require.Equal(t, 1, strings.Count(text, e.old), "fixture %s must contain %q once", e.file, e.old)
		files[e.file] = strings.Replace(text, e.old, e.new, 1)
	}
	return loadFiles(files)
}

func loadFiles(files map[string]string) (*BknNetwork, error) {
	mfs := NewMemoryFileSystem()
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		mfs.AddFile("/net/"+path, []byte(files[path]))
	}
	return LoadNetworkWithFS(mfs, "/net")
}

type v3Edit struct {
	file, old, new string
}
