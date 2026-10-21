// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	fValueType = "value_types/pod_phase.bkn"

	valueTypeFile = "---\ntype: value_type\nid: pod_phase\nname: Pod 阶段\ntags: [k8s]\n---\n\n## ValueType: Pod 阶段\n\nPod 生命周期阶段。\n\n" +
		"### Definition\n\n```yaml\nbase_type: keyword\nenum:\n" +
		"  - { value: Pending, display: 等待 }\n  - { value: Running, display: 运行中 }\n" +
		"  - { value: Unknown, display: 未知, deprecated: true, description: 旧版本上报 }\n```\n"

	dpPhase    = "| phase | 阶段 | keyword | | | | phase |"
	pcPhase    = "phase:\n  value_type: pod_phase\n"
	dpHeader   = "| Name | Display Name | Type | Semantic | Required | Description | Mapped Field |\n|------|--------------|------|----------|----------|-------------|--------------|\n"
	dpAliasHdr = "| Name | Display Name | Type | Semantic | Required | Description | Mapped Field | Aliases |\n|------|--------------|------|----------|----------|-------------|--------------|---------|\n"
)

// withDeclarations adds a value type that a new property references, and aliases on the object type,
// a property and the action type.
func withDeclarations(extra ...v3Edit) []v3Edit {
	return append([]v3Edit{
		{file: fValueType, new: valueTypeFile},
		{fObj, dpRow, dpRow + "\n" + dpPhase},
		{fObj, pcBlock, pcBlock + pcPhase},
		{fObj, "tags: [k8s]\n", "tags: [k8s]\naliases: [容器组]\n"},
		{fObj, dpHeader, dpAliasHdr},
		{fObj, dpPodName, "| pod_name | 名称 | string | | true | | pod_name | 名字, Pod 名 |"},
		{fAct, "tags: []\naction_intent", "tags: []\naliases: [重启容器组]\naction_intent"},
	}, extra...)
}

// definitionFile is a minimal 3.0 file of another object type or action type, to test names across
// definitions.
func definitionFile(kind, id, name string) string {
	return "---\ntype: " + kind + "\nid: " + id + "\nname: " + name + "\ntags: []\n---\n\n## " + headingPrefix[kind] + ": " + name + "\n"
}

func TestLoadV3_ReadsValueTypesAndAliases(t *testing.T) {
	net, err := loadV3(t, withDeclarations()...)
	require.NoError(t, err)

	require.Len(t, net.ValueTypes, 1)
	vt := net.ValueTypes[0]
	assert.Equal(t, "pod_phase", vt.ID)
	assert.Equal(t, "Pod 阶段", vt.Name)
	assert.Equal(t, []string{"k8s"}, vt.Tags)
	assert.Equal(t, "Pod 生命周期阶段。", vt.Description)
	assert.Equal(t, "keyword", vt.BaseType)
	assert.Equal(t, []*ValueTypeEnumItem{
		{Value: "Pending", Display: "等待"},
		{Value: "Running", Display: "运行中"},
		{Value: "Unknown", Display: "未知", Deprecated: true, Description: "旧版本上报"},
	}, vt.Enum)

	ot := net.ObjectTypes[0]
	assert.Equal(t, []string{"容器组"}, ot.Aliases)
	props := map[string]*DataProperty{}
	for _, p := range ot.DataProperties {
		props[p.Name] = p
	}
	assert.Equal(t, []string{"名字", "Pod 名"}, props["pod_name"].Aliases)
	assert.Equal(t, &PropertyConstraints{ValueType: "pod_phase"}, props["phase"].Constraints)
	assert.Equal(t, []string{"重启容器组"}, net.ActionTypes[0].Aliases)
}

// Under 2.x value_types/ is never read and the new frontmatter field and column are unknown, exactly
// as before 3.0 existed.
func TestLoadLegacy_IgnoresValueTypesAndAliases(t *testing.T) {
	net, err := loadV3(t, withDeclarations(v3Edit{fNet, "bkn_spec: \"3.0\"\n", ""})...)
	require.NoError(t, err)
	assert.Empty(t, net.ValueTypes)
	assert.Empty(t, net.ObjectTypes[0].Aliases)
	assert.Empty(t, net.ActionTypes[0].Aliases)
	for _, p := range net.ObjectTypes[0].DataProperties {
		assert.Empty(t, p.Aliases)
	}
}

func TestLoadV3_RefusesWrongValueTypesAndAliases(t *testing.T) {
	definition := func(old, new string) v3Edit { return v3Edit{fValueType, old, new} }
	const enumBlock = "enum:\n  - { value: Pending, display: 等待 }\n  - { value: Running, display: 运行中 }\n" +
		"  - { value: Unknown, display: 未知, deprecated: true, description: 旧版本上报 }\n"
	cases := []struct {
		name  string
		edits []v3Edit
		want  string
	}{
		// value types
		{"base_type", []v3Edit{definition("base_type: keyword", "base_type: text")}, `value type "pod_phase": base_type must be one of string, keyword`},
		{"no base_type", []v3Edit{definition("base_type: keyword\n", "")}, `base_type must be one of`},
		{"no Definition", []v3Edit{definition("### Definition", "### Notes")}, `section "### Notes" is unknown; the value_type sections are "### Definition"`},
		{"empty Definition", []v3Edit{definition("```yaml\nbase_type: keyword\n"+enumBlock+"```\n", "")}, `value type "pod_phase" needs a ### Definition section`},
		{"unknown key", []v3Edit{definition("base_type: keyword\n", "base_type: keyword\nscale: 2\n")}, `value type "pod_phase" scale is not a value type key; the keys are base_type, enum, range, length, pattern, unit`},
		{"enum with pattern", []v3Edit{definition("base_type: keyword\n", "base_type: keyword\npattern: \"^P\"\n")}, "an enum lists the valid values, so range, length and pattern do not go with it"},
		{"enum is not a list", []v3Edit{definition(enumBlock, "enum: Pending\n")}, "enum must be a list of items"},
		{"empty enum", []v3Edit{definition(enumBlock, "enum: []\n")}, "enum lists no items"},
		{"enum item key", []v3Edit{definition("display: 等待 }", "display: 等待, label: x }")}, `enum item 1 key "label" is unknown`},
		{"enum value twice", []v3Edit{definition("value: Running", "value: Pending")}, `enum item 2: value "Pending" is the same as item 1`},
		{"enum value twice as a number", []v3Edit{definition("base_type: keyword", "base_type: decimal"), definition("value: Pending", "value: 1"), definition("value: Running", "value: 1.0"), definition("value: Unknown", "value: 2")}, `value "1.0" is the same as item 1`},
		{"enum value of another type", []v3Edit{definition("base_type: keyword", "base_type: integer"), definition("value: Pending", "value: 1"), definition("value: Running", "value: 2")}, `enum item 3: value "Unknown" does not fit base_type integer`},
		{"enum without display", []v3Edit{definition(", display: 运行中", "")}, "enum item 2 needs a display"},
		{"deprecated that is not a bool", []v3Edit{definition("deprecated: true", "deprecated: yes")}, "deprecated must be true or false"},
		{"unit on a keyword", []v3Edit{definition("base_type: keyword\n", "base_type: keyword\nunit: 个\n")}, `unit needs a numeric Type, got "keyword"`},
		{"range min above max", []v3Edit{definition("base_type: keyword\n"+enumBlock, "base_type: integer\nrange: { min: 5, max: 1 }\n"), {fObj, dpPhase, "| phase | 阶段 | integer | | | | phase |"}}, "range min 5 is greater than max 1"},
		{"value type field", []v3Edit{definition("tags: [k8s]\n", "tags: [k8s]\naliases: [阶段]\n")}, `frontmatter field "aliases" is not a value_type field`},
		{"value type heading", []v3Edit{definition("## ValueType: Pod 阶段", "## Pod 阶段")}, `write "## ValueType: Pod 阶段"`},
		{"value type name twice", []v3Edit{{file: "value_types/other.bkn", new: strings.ReplaceAll(valueTypeFile, "id: pod_phase", "id: other")}}, `are both named "Pod 阶段"; a value type name is unique in the network`},
		// value_type references
		{"value type not defined", []v3Edit{{fObj, pcPhase, "phase:\n  value_type: node_phase\n"}}, `object type "pod" data property "phase": value type "node_phase" is not defined in this network`},
		{"base_type differs from Type", []v3Edit{{fObj, dpPhase, "| phase | 阶段 | string | | | | phase |"}}, `value type "pod_phase" has base_type keyword, the property Type is "string"`},
		{"value_type with an inline rule", []v3Edit{{fObj, pcPhase, pcPhase + "  length: { max: 8 }\n"}}, `property constraints "phase": the property references value type "pod_phase", which holds its value rules`},
		{"value_type that is not an id", []v3Edit{{fObj, pcPhase, "phase:\n  value_type: [pod_phase]\n"}}, `property constraints "phase" value_type must be the id of a value type`},
		// aliases
		{"aliases as one string", []v3Edit{{fObj, "aliases: [容器组]", "aliases: 容器组"}}, "frontmatter aliases must be a YAML list"},
		{"null alias", []v3Edit{{fObj, "aliases: [容器组]", "aliases: [~]"}}, "frontmatter aliases must list names"},
		{"empty alias", []v3Edit{{fObj, "aliases: [容器组]", "aliases: [\" \"]"}}, "object type aliases: an alias is empty"},
		{"empty alias in a cell", []v3Edit{{fObj, "名字, Pod 名", "名字,, Pod 名"}}, `data property "pod_name" aliases: an alias is empty`},
		{"full-width comma", []v3Edit{{fObj, "名字, Pod 名", "名字，Pod 名"}}, `alias "名字，Pod 名" holds a full-width comma; separate aliases with a half-width comma ,`},
		{"alias too long", []v3Edit{{fObj, "aliases: [容器组]", "aliases: [" + strings.Repeat("容", 41) + "]"}}, "is longer than 40 characters"},
		{"too many aliases", []v3Edit{{fObj, "aliases: [容器组]", "aliases: [a1, a2, a3, a4, a5, a6, a7, a8, a9, a10, a11]"}}, "object type aliases: 11 aliases, at most 10"},
		{"alias twice", []v3Edit{{fObj, "名字, Pod 名", "名字, 名字"}}, `alias "名字" appears twice`},
		{"alias is the own name", []v3Edit{{fObj, "aliases: [容器组]", "aliases: [Pod]"}}, `object type aliases: alias "Pod" is the same as its own name`},
		{"alias is the own display name", []v3Edit{{fObj, "名字, Pod 名", "名称"}}, `data property "pod_name" aliases: alias "名称" is the same as its own name`},
		{"alias is another property", []v3Edit{{fObj, "名字, Pod 名", "CPU"}}, `data property "pod_name" aliases: alias "CPU" is the name of data property "cpu_pct"`},
		{"alias is another object type", []v3Edit{{file: "object_types/node.bkn", new: definitionFile(kindObjectType, "node", "节点")}, {fObj, "aliases: [容器组]", "aliases: [节点]"}}, `object type "pod" alias "节点" is the name of object type "node"`},
		{"action alias is the own name", []v3Edit{{fAct, "aliases: [重启容器组]", "aliases: [重启Pod]"}}, `action type aliases: alias "重启Pod" is the same as its own name`},
		{"alias is another action type", []v3Edit{{file: "action_types/stop_pod.bkn", new: definitionFile(kindActionType, "stop_pod", "停止Pod")}, {fAct, "aliases: [重启容器组]", "aliases: [停止Pod]"}}, `action type "restart_pod" alias "停止Pod" is the name of action type "stop_pod"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := loadV3(t, withDeclarations(c.edits...)...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.want)
			var specErr *SpecError
			assert.ErrorAs(t, err, &specErr)
		})
	}
}

// The checks import runs are the ones a save through the API runs.
func TestValidateValueTypeAndNetworkDeclarations(t *testing.T) {
	vt := &BknValueType{
		BknValueTypeFrontmatter: BknValueTypeFrontmatter{ID: "dept_code", Name: "科室代码"},
		BaseType:                "string",
		Enum:                    []*ValueTypeEnumItem{{Value: "0101", Display: "心内科"}, {Value: "0102", Display: "呼吸内科"}},
	}
	require.NoError(t, ValidateValueType(vt))
	vt.Enum[1].Value = "0101"
	assert.ErrorContains(t, ValidateValueType(vt), `value "0101" is the same as item 1`)

	pct := &BknValueType{BknValueTypeFrontmatter: BknValueTypeFrontmatter{ID: "percent", Name: "百分比"},
		BaseType: "decimal", Range: &ValueRange{Min: int64(0), Max: int64(100)}, Unit: "%"}
	require.NoError(t, ValidateValueType(pct))

	net := &BknNetwork{
		ValueTypes: []*BknValueType{pct},
		ObjectTypes: []*BknObjectType{{
			BknObjectTypeFrontmatter: BknObjectTypeFrontmatter{ID: "pod", Name: "Pod"},
			DataProperties:           []*DataProperty{{Name: "cpu", Type: "decimal", Constraints: &PropertyConstraints{ValueType: "percent"}}},
		}},
	}
	require.NoError(t, ValidateNetworkDeclarations(net))
	net.ObjectTypes[0].DataProperties[0].Type = "float"
	assert.ErrorContains(t, ValidateNetworkDeclarations(net), `value type "percent" has base_type decimal, the property Type is "float"`)

	at := &BknActionType{BknActionTypeFrontmatter: BknActionTypeFrontmatter{Name: "重启Pod", Aliases: []string{"重启Pod"}}}
	assert.ErrorContains(t, ValidateActionTypeDeclarations(at), "is the same as its own name")
}

func TestWriteV3_ValueTypesAndAliasesRoundTrip(t *testing.T) {
	net, err := loadV3(t, withDeclarations()...)
	require.NoError(t, err)
	first := exportFiles(t, net)

	again, err := loadFiles(first)
	require.NoError(t, err, "the exporter's own output is valid 3.0")
	assert.Equal(t, first, exportFiles(t, again))
}

func TestWriteV3_WritesValueTypesAndAliases(t *testing.T) {
	net, err := loadV3(t, withDeclarations()...)
	require.NoError(t, err)
	files := exportFiles(t, net)

	assert.Equal(t, "---\ntype: value_type\nid: pod_phase\nname: Pod 阶段\ntags: [k8s]\n---\n\n## ValueType: Pod 阶段\n\nPod 生命周期阶段。\n\n"+
		"### Definition\n\n```yaml\nbase_type: keyword\nenum:\n  - {value: Pending, display: 等待}\n  - {value: Running, display: 运行中}\n"+
		"  - {value: Unknown, display: 未知, deprecated: true, description: 旧版本上报}\n```\n", files[fValueType])

	obj := files[fObj]
	assert.Contains(t, obj, "tags: [k8s]\naliases: [容器组]\n---\n")
	assert.Contains(t, obj, "| Name | Display Name | Type | Semantic | Required | Aliases | Description | Mapped Field |\n")
	assert.Contains(t, obj, "| pod_name | 名称 | string |  | true | 名字, Pod 名 |  | pod_name |\n")
	assert.Contains(t, obj, "phase:\n  value_type: pod_phase\n")
	assert.Contains(t, files[fAct], "tags: []\naliases: [重启容器组]\naction_intent: modify\n")

	network := files[fNet]
	assert.Contains(t, network, "### Value Types\n\n| ID | Name | File Path | Description |\n|----|------|-----------|-------------|\n"+
		"| pod_phase | Pod 阶段 | `value_types/pod_phase.bkn` | Pod 生命周期阶段。 |\n")
	assert.Contains(t, network, "└── value_types/\n    └── pod_phase.bkn\n```\n")
	assert.Regexp(t, `(?m)^value_type:pod_phase  sha256:[0-9a-f]+$`, files[ChecksumFileName])
}

// Enum values keep their kind: a numeric base type writes numbers, any other a string, so a code
// such as 0101 keeps its leading zero.
func TestWriteV3_EnumValuesKeepTheirKind(t *testing.T) {
	net, err := loadV3(t, withDeclarations(
		v3Edit{fValueType, "base_type: keyword", "base_type: string"},
		v3Edit{fValueType, "value: Pending", "value: \"0101\""},
		v3Edit{fValueType, "value: Running", "value: \"1.0\""},
		v3Edit{fObj, dpPhase, "| phase | 阶段 | string | | | | phase |"},
	)...)
	require.NoError(t, err)
	assert.Equal(t, "0101", net.ValueTypes[0].Enum[0].Value)
	vtFile := exportFiles(t, net)[fValueType]
	assert.Contains(t, vtFile, "- {value: \"0101\", display: 等待}\n  - {value: \"1.0\", display: 运行中}\n")

	net, err = loadV3(t, withDeclarations(
		v3Edit{fValueType, "base_type: keyword", "base_type: integer"},
		v3Edit{fValueType, "value: Pending", "value: 1"},
		v3Edit{fValueType, "value: Running", "value: 2"},
		v3Edit{fValueType, "value: Unknown", "value: 9"},
		v3Edit{fObj, dpPhase, "| phase | 阶段 | integer | | | | phase |"},
	)...)
	require.NoError(t, err)
	assert.Contains(t, exportFiles(t, net)[fValueType], "- {value: 1, display: 等待}\n  - {value: 2, display: 运行中}\n")
}

// A 2.x package that happens to hold a value_types/ file keeps its CHECKSUM as before: the file is
// not a definition there.
func TestChecksum_ValueTypeLineOnlyIn3(t *testing.T) {
	mfs := NewMemoryFileSystem()
	mfs.AddFile("/net/network.bkn", []byte(strings.Replace(v3Files[fNet], "bkn_spec: \"3.0\"\n", "", 1)))
	mfs.AddFile("/net/"+fValueType, []byte(valueTypeFile))
	legacy, err := GenerateChecksumFileWithFS(mfs, "/net")
	require.NoError(t, err)
	assert.NotContains(t, legacy, "value_type:")

	mfs.AddFile("/net/network.bkn", []byte(v3Files[fNet]))
	current, err := GenerateChecksumFileWithFS(mfs, "/net")
	require.NoError(t, err)
	assert.Contains(t, current, "value_type:pod_phase  sha256:")
	ok, msgs := VerifyChecksumFileWithFS(mfs, "/net")
	assert.True(t, ok, msgs)
}

// The frontmatter rules of §4 apply to value types as to every definition: an id such as a/b would
// be exported as a nested file the next import skips.
func TestValidateNetwork_ChecksValueTypeFrontmatter(t *testing.T) {
	vt := func(id, name string) *BknValueType {
		return &BknValueType{BknValueTypeFrontmatter: BknValueTypeFrontmatter{Type: kindValueType, ID: id, Name: name}, BaseType: "keyword"}
	}
	net := &BknNetwork{
		BknNetworkFrontmatter: BknNetworkFrontmatter{Type: kindNetwork, ID: "net", Name: "Net"},
		ValueTypes:            []*BknValueType{vt("a/b", "甲"), vt("phase", ""), vt("dup", "乙"), vt("dup", "乙")},
	}
	errs := map[string]string{}
	for _, e := range ValidateNetwork(net).Errors {
		errs[e.Table+" "+e.Code] = e.Message
	}
	assert.Contains(t, errs, "value_type:a/b invalid_id")
	assert.Contains(t, errs, "value_type:phase missing_frontmatter_field")
	assert.Contains(t, errs, "network.bkn duplicate_id")
	assert.Contains(t, errs, "network.bkn duplicate_name")
}
