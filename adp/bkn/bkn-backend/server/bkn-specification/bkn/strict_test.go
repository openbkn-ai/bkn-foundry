// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	fNet  = "network.bkn"
	fObj  = "object_types/pod.bkn"
	fRel  = "relation_types/pod_peer.bkn"
	fAct  = "action_types/restart_pod.bkn"
	fRisk = "risk_types/restart_risk.bkn"
	fCG   = "concept_groups/k8s.bkn"
)

func TestLoadV3_FixtureIsValid(t *testing.T) {
	net, err := loadV3(t)
	require.NoError(t, err)
	assert.Equal(t, BknSpecV3, net.BknSpec)
	require.Len(t, net.ObjectTypes, 1)
	assert.Equal(t, "Pod 描述。", net.ObjectTypes[0].Description)
	assert.True(t, ValidateNetwork(net).OK(), "%+v", ValidateNetwork(net).Errors)
	require.Len(t, net.ActionTypes, 1)
	assert.Equal(t, "modify", net.ActionTypes[0].ActionIntent)
	assert.Equal(t, "重启会中断服务。\n\n### 控制策略\n\n维护窗口内执行。", net.RiskTypes[0].Description,
		"a risk type has no known sections: its ### headings are description")
}

// Each row of the SPECIFICATION.md §1.3 table is refused under 3.0, with a message that names the
// place and the one accepted spelling.
func TestLoadV3_RefusesEveryNonCanonicalForm(t *testing.T) {
	cases := []struct {
		row  string
		edit v3Edit
		want string
	}{
		{"frontmatter field not listed", v3Edit{fObj, "tags: [k8s]\n", "tags: [k8s]\nenabled: true\n"}, `frontmatter field "enabled" is not a object_type field`},
		{"bkn_spec in a definition file", v3Edit{fObj, "tags: [k8s]\n", "tags: [k8s]\nbkn_spec: \"3.0\"\n"}, `frontmatter field "bkn_spec" is not a object_type field`},
		{"network field not listed", v3Edit{fNet, "tags: []\n", "tags: []\nbusiness_domain: x\n"}, `frontmatter field "business_domain" is not a knowledge_network field`},
		{"type differs from the directory", v3Edit{fObj, "type: object_type", "type: relation_type"}, `frontmatter type "relation_type" does not match the directory; write "object_type"`},
		{"network type", v3Edit{fNet, "type: knowledge_network", "type: network"}, `frontmatter type "network" does not match the directory; write "knowledge_network"`},
		{"tags as one string", v3Edit{fObj, "tags: [k8s]", "tags: k8s"}, "frontmatter tags must be a YAML list"},
		{"heading prefix", v3Edit{fObj, "## ObjectType: Pod", "## Object: Pod"}, `definition heading "## Object: Pod" does not match; write "## ObjectType: Pod"`},
		{"heading name", v3Edit{fRel, "## RelationType: 同组", "## RelationType: 属于"}, `write "## RelationType: 同组"`},
		{"unknown section", v3Edit{fObj, "### Keys", "### Notes\n\n备注\n\n### Keys"}, `section "### Notes" is unknown`},
		{"description line starting with #", v3Edit{fObj, "Pod 描述。", "Pod 描述。\n\n#### 说明"}, `description line 3 starts with #, which reads as a heading; write it as \#`},
		{"network description heading", v3Edit{fNet, "运维网络。", "运维网络。\n### 背景"}, `description line 2 starts with #`},
		{"network section", v3Edit{fNet, "运维网络。\n", "运维网络。\n\n## Notes\n"}, `network.bkn section "## Notes" is unknown`},
		{"unknown column", v3Edit{fObj, "| Name | Display Name | Type | Semantic |", "| Name | Display Name | Kind | Semantic |"}, `section "### Data Properties" column "Kind" is unknown`},
		{"duplicate column", v3Edit{fRel, "| Source | Target | Type |", "| Source | Target | Source |"}, `column "Source" appears twice`},
		{"missing separator row", v3Edit{fRel, "| Source | Target | Type |\n|--------|--------|------|\n", "| Source | Target | Type |\n"}, "table is missing the separator row"},
		{"row broken by a line break", v3Edit{fAct, "| pod | string | Body | | property | pod_name | Pod 名称 |", "| pod | string | Body | | property | pod_name | Pod\n名称 |"}, "does not end with |; a line break inside a cell is written as <br>"},
		{"more cells than the header", v3Edit{fRel, "| pod | pod | direct |", "| pod | pod | direct | extra |"}, "row 1 has 4 cells, more than the 3 columns"},
		{"single-row table with two rows", v3Edit{fObj, "| resource | res_pod | pods |", "| resource | res_pod | pods |\n| resource | res_x | x |"}, `section "### Data Source" holds 2 rows; it reads one row`},
		{"second table", v3Edit{fRel, "| pod_name | pod_name |\n", "| pod_name | pod_name |\n\n| Source Property | Target Property |\n|-----------------|-----------------|\n"}, "holds a second table"},
		{"code block in a table section", v3Edit{fRel, "| pod_name | pod_name |\n", "| pod_name | pod_name |\n\n```yaml\na: 1\n```\n"}, "holds a table, not a code block"},
		{"second yaml block", v3Edit{fAct, "operation: not_empty\n```\n", "operation: not_empty\n```\n\n```yaml\nfield: x\n```\n"}, "holds a second code block"},
		{"invalid yaml", v3Edit{fAct, "operation: not_empty\n", "operation: [not_empty\n"}, `section "### Trigger Condition" yaml is invalid`},
		{"yaml without a fence", v3Edit{fAct, "```yaml\nfield: pod_name\noperation: not_empty\n```\n", "field: pod_name\noperation: not_empty\n"}, `section "### Trigger Condition" holds no yaml code block`},
		{"yaml in a yml fence", v3Edit{fObj, "```yaml\ncpu_pct:", "```yml\ncpu_pct:"}, `section "### Property Constraints" holds no yaml code block`},
		{"section twice", v3Edit{fObj, "### Keys", "### Data Properties\n\n| Name | Display Name | Type | Description | Mapped Field |\n|------|--------------|------|-------------|--------------|\n| x | X | string | | x |\n\n### Keys"}, `section "### Data Properties" appears twice`},
		{"risk section twice", v3Edit{fRisk, "### 控制策略\n\n维护窗口内执行。\n", "### 控制策略\n\n维护窗口内执行。\n\n### 控制策略\n\n夜间执行。\n"}, `section "### 控制策略" appears twice`},
		{"action_type field", v3Edit{fAct, "action_intent: modify", "action_type: modify"}, `frontmatter field "action_type" is not a 3.0 field; write "action_intent"`},
		{"Action Type column", v3Edit{fAct, "| Bound Object |\n|--------------|\n| pod |", "| Bound Object | Action Type |\n|--------------|-------------|\n| pod | modify |"}, `column "Action Type" is not a 3.0 column; write the frontmatter field action_intent`},
		{"Tool Configuration", v3Edit{fAct, "### Action Source", "### Tool Configuration"}, `section "### Tool Configuration" is not a 3.0 section; write "### Action Source"`},
		{"Action Source synonym column", v3Edit{fAct, "| Type | BoxID | ToolID |", "| Type | Toolbox ID | ToolID |"}, `column "Toolbox ID" is not a 3.0 column; write "BoxID"`},
		{"flat logic property table", v3Edit{fObj, "#### cpu\n", "| Name | Display Name | Type | Description |\n|------|--------------|------|-------------|\n| x | X | metric | |\n\n#### cpu\n"}, "uses the flat table"},
		{"logic Source synonym column", v3Edit{fObj, "| Source Type | Source ID | Source Name |", "| Source Type | Source ID | Tool ID |"}, `logic property "cpu" **Source** column "Tool ID" is not a 3.0 column; write "ToolID"`},
		{"parameter synonym column", v3Edit{fAct, "| Name | Type | Source | Operation | ValueFrom | Value | Description |", "| Parameter | Type | Source | Operation | ValueFrom | Value | Description |"}, `column "Parameter" is not a 3.0 column; write "Name"`},
		{"logic parameter Binding column", v3Edit{fObj, "| Name | Type | Source | Operation | ValueFrom | Value | Description |\n|------|------|--------|-----------|-----------|-------|-------------|\n| pod |", "| Name | Type | Source | Operation | Binding | Value | Description |\n|------|------|--------|-----------|-----------|-------|-------------|\n| pod |"}, `column "Binding" is not a 3.0 column; write ValueFrom and Value`},
		{"legacy parameter layout", v3Edit{fAct,
			"| Name | Type | Source | Operation | ValueFrom | Value | Description |\n|------|------|--------|-----------|-----------|-------|-------------|\n| pod | string | Body | | property | pod_name | Pod 名称 |\n| reason | string | Body | | input | | 原因 |",
			"| Parameter | Type | Source | Binding | Description |\n|-----------|------|--------|---------|-------------|\n| pod | string | property | pod_name | Pod 名称 |"},
			`uses the legacy layout Parameter | Source | Binding; write Name | Type | Source | Operation | ValueFrom | Value | Description, with the value source in ValueFrom, e.g. row "pod" as ValueFrom "property", Value "pod_name"`},
		{"wrapped condition", v3Edit{fAct, "field: pod_name\noperation: not_empty\n", "condition:\n  field: pod_name\n  operation: not_empty\n"}, `wraps the condition in "condition:"; write the condition itself`},
		{"impact contracts without the key", v3Edit{fAct, "impact_contracts:\n  - object_type_id: pod\n    expected_operation: modify\n", "- object_type_id: pod\n  expected_operation: modify\n"}, "must be a list under the single key impact_contracts:"},
		{"Keys line", v3Edit{fObj, "Display Key: pod_name\n", "Display Key: pod_name\nSort Key: id\n"}, `section "### Keys" line 3 is not a key line`},
		{"logic property label", v3Edit{fObj, "**Source**", "**Origin**"}, `logic property "cpu" label "**Origin**" is unknown`},
		{"relation mapping section of another Type", v3Edit{fRel, "### Mapping Rules", "### Backing Resource\n\n| Type | ID |\n|------|----|\n| resource | r |\n\n### Mapping Rules"}, `section "### Backing Resource" belongs to indirect relations, but Endpoint Type is direct; write "### Mapping Rules"`},
		{"concept group column", v3Edit{fCG, "| ID | Name | Description |", "| ID | Name | Note |"}, `section "### Object Types" column "Note" is unknown`},
	}
	for _, c := range cases {
		t.Run(c.row, func(t *testing.T) {
			_, err := loadV3(t, c.edit)
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.want)
			var specErr *SpecError
			assert.ErrorAs(t, err, &specErr, "importers recognize a spec refusal through the wrapping")
		})
	}
}

// 3.0 syntax of a later phase is refused instead of being dropped.
func TestLoadV3_RefusesSyntaxNotSupportedYet(t *testing.T) {
	cases := []struct {
		name string
		edit v3Edit
		want string
	}{
		{"relation aliases", v3Edit{fRel, "name: 同组\n", "name: 同组\naliases: [同伴]\n"}, `frontmatter field "aliases" is not supported by this version yet`},
		{"lifecycle", v3Edit{fAct, "action_intent: modify\n", "action_intent: modify\nlifecycle:\n  status: deprecated\n  reason: x\n"}, `frontmatter field "lifecycle" is not supported by this version yet`},
		{"reverse_name", v3Edit{fRel, "name: 同组\n", "name: 同组\nreverse_name: 同组\n"}, `frontmatter field "reverse_name" is not supported by this version yet`},
		{"importance", v3Edit{fRel, "name: 同组\n", "name: 同组\nimportance: secondary\n"}, `frontmatter field "importance" is not supported by this version yet`},
		{"fallback_for", v3Edit{fRel, "name: 同组\n", "name: 同组\nfallback_for: x\n"}, `frontmatter field "fallback_for" is not supported by this version yet`},
		{"verification", v3Edit{fRel, "name: 同组\n", "name: 同组\nverification: unconfirmed\n"}, `frontmatter field "verification" is not supported by this version yet`},
		{"Cardinality", v3Edit{fRel, "| Source | Target | Type |\n|--------|--------|------|\n| pod | pod | direct |", "| Source | Target | Type | Cardinality |\n|--------|--------|------|-------------|\n| pod | pod | direct | many_to_one |"}, `column "Cardinality" is not supported by this version yet`},
		{"Relation Constraints", v3Edit{fRel, "### Mapping Rules", "### Relation Constraints\n\n```yaml\nrequired: [target]\n```\n\n### Mapping Rules"}, `section "### Relation Constraints" is not supported by this version yet`},
		{"relation properties", v3Edit{fRel, "### Mapping Rules", "### Data Properties\n\n| Name | Display Name | Type |\n|------|--------------|------|\n\n### Mapping Rules"}, `section "### Data Properties" is not supported by this version yet`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := loadV3(t, c.edit)
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.want)
			var specErr *SpecError
			assert.ErrorAs(t, err, &specErr)
		})
	}
}

// The exceptions of §1.3 stay readable: content the exporter generates and the synonym operators.
func TestLoadV3_KeepsTheExceptions(t *testing.T) {
	_, err := loadV3(t,
		v3Edit{fNet, "运维网络。\n", "运维网络。\n\n## Network Overview\n\n### Object Types\n\n| ID | Name | File Path | Description |\n|----|------|-----------|-------------|\n\n## Directory Structure\n\n```\n.\n```\n"},
		v3Edit{fAct, "operation: not_empty", "operation: \"!=\""},
	)
	require.NoError(t, err)
}

// A 2.x package keeps reading every form 3.0 refuses.
func TestLoadLegacy_IgnoresTheV3Rules(t *testing.T) {
	_, err := loadV3(t,
		v3Edit{fNet, "bkn_spec: \"3.0\"\n", ""},
		v3Edit{fObj, "tags: [k8s]\n", "tags: k8s\nenabled: true\n"},
		v3Edit{fObj, "### Keys", "### Notes\n\n备注\n\n### Keys"},
		v3Edit{fAct, "### Action Source", "### Tool Configuration"},
		v3Edit{fAct, "action_intent: modify", "action_type: modify"},
	)
	require.NoError(t, err)
}

func TestLoadV3_ReadsFrontmatterStringsAsWritten(t *testing.T) {
	net, err := loadV3(t,
		v3Edit{fObj, "id: pod\nname: Pod\ntags: [k8s]", "id: \"001\"\nname: 1.0\ntags: [007, x]"},
		v3Edit{fObj, "## ObjectType: Pod", "## ObjectType: 1.0"},
		v3Edit{fObj, "Primary Keys: id", "Primary Keys: id"},
	)
	require.NoError(t, err)
	ot := net.ObjectTypes[0]
	assert.Equal(t, "001", ot.ID)
	assert.Equal(t, "1.0", ot.Name)
	assert.Equal(t, []string{"007", "x"}, ot.Tags)
}

func TestLoadV3_UnescapesDescriptionHeadings(t *testing.T) {
	net, err := loadV3(t, v3Edit{fObj, "Pod 描述。", "Pod 描述。\n\n\\### 标题\n\\\\# 两个反斜杠"})
	require.NoError(t, err)
	assert.Equal(t, "Pod 描述。\n\n### 标题\n\\# 两个反斜杠", net.ObjectTypes[0].Description)
}
