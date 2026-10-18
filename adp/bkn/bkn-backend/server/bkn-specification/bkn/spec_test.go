// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeBknSpec(t *testing.T) {
	for raw, want := range map[any]string{
		nil:        BknSpecLegacy,
		"":         BknSpecLegacy,
		"2.0":      BknSpecLegacy,
		"2.0.1":    BknSpecLegacy,
		"3.0":      BknSpecV3,
		"3":        BknSpecV3,
		"3.0.2":    BknSpecV3,
		3:          BknSpecV3,
		float64(3): BknSpecV3, // 3.0 written bare is read by YAML as a number
	} {
		got, err := normalizeBknSpec(raw)
		require.NoError(t, err, "%v", raw)
		assert.Equal(t, want, got, "%v", raw)
	}
	for _, raw := range []any{"4.0", "3.1", "1.0", "v3", "abc", []any{"3.0"}} {
		_, err := normalizeBknSpec(raw)
		assert.Error(t, err, "%v", raw)
	}
}

func TestParseNetworkFile_BknSpec(t *testing.T) {
	net, err := ParseNetworkFile("---\ntype: knowledge_network\nid: kn\nname: kn\nbkn_spec: 3.0\n---\n\n# kn\n", "network.bkn")
	require.NoError(t, err)
	assert.Equal(t, BknSpecV3, net.BknSpec)

	_, err = ParseNetworkFile("---\ntype: knowledge_network\nid: kn\nname: kn\nbkn_spec: \"4.0\"\n---\n", "network.bkn")
	assert.ErrorContains(t, err, "unsupported bkn_spec")
}

const specTestObjectType = `---
type: object_type
id: pod
name: Pod
---

## ObjectType: Pod

### Data Properties

| Name | Display Name | Type | Description | Mapped Field |
|------|--------------|------|-------------|--------------|
| id | ID | string | | id |
| pod_name | 名称 | string | | pod_name |

### Logic Properties

#### cpu

**Meta**

| Display Name | Type | Description |
|--------------|------|-------------|
| CPU | metric | |

**Source**

| Source Type | Source ID | Source Name |
|-------------|-----------|-------------|
| metric | m_cpu | |

**Parameters**

| Name | Type | Source | Operation | ValueFrom | Value | Description |
|------|------|--------|-----------|-----------|-------|-------------|
%s

### Keys

Primary Keys: id
Display Key: pod_name
`

const specTestActionType = `---
type: action_type
id: restart_pod
name: 重启Pod
---

## ActionType: 重启Pod

重启异常 Pod。

### Bound Object

| Bound Object |
|--------------|
| pod |

### Tool Configuration

| Type | Toolbox ID | Tool ID |
|------|------------|---------|
| tool | box_k8s | tool_restart |

### Parameter Binding

%s
`

const legacyParameterTable = `| Parameter | Type | Source | Binding | Description |
|-----------|------|--------|---------|-------------|
| pod | string | property | pod_name | Pod 名称 |
| reason | string | input | - | 原因 |`

const goodLogicParameter = `| pod | string | | == | property | pod_name | |`

// loadSpecNetwork loads a one-object, one-action network whose network.bkn carries the given
// frontmatter line (empty for none).
func loadSpecNetwork(t *testing.T, specLine, logicParameterRow, parameterTable string) (*BknNetwork, error) {
	t.Helper()
	mfs := NewMemoryFileSystem()
	mfs.AddFile("/net/network.bkn", []byte("---\ntype: knowledge_network\nid: kn\nname: kn\n"+specLine+"---\n\n# kn\n"))
	mfs.AddFile("/net/object_types/pod.bkn", []byte(fmt.Sprintf(specTestObjectType, logicParameterRow)))
	mfs.AddFile("/net/action_types/restart_pod.bkn", []byte(fmt.Sprintf(specTestActionType, parameterTable)))
	return LoadNetworkWithFS(mfs, "/net")
}

// A package that declares no bkn_spec reads exactly as it did before the field existed, including
// the behavior 3.0 corrects. These assertions are the compatibility promise.
func TestLoadNetwork_LegacySpecKeepsCurrentBehavior(t *testing.T) {
	misplaced := `| pod | string | property | | pod_name | | |`
	net, err := loadSpecNetwork(t, "", misplaced, legacyParameterTable)
	require.NoError(t, err)
	assert.Equal(t, BknSpecLegacy, net.BknSpec)

	at := net.ActionTypes[0]
	require.NotNil(t, at.ActionSource)
	assert.Equal(t, "tool_restart", at.ActionSource.ToolID)
	assert.Contains(t, at.Description, "### Tool Configuration")

	require.Len(t, at.Parameters, 2)
	assert.Equal(t, "property", at.Parameters[0].Source)
	assert.Equal(t, "pod_name", at.Parameters[0].ValueFrom)

	lp := net.ObjectTypes[0].LogicProperties[0]
	assert.Equal(t, "property", lp.Parameters[0].Source)
	assert.Equal(t, "pod_name", lp.Parameters[0].ValueFrom)
}

func TestLoadNetwork_V3ReadsCorrectedRules(t *testing.T) {
	net, err := loadSpecNetwork(t, "bkn_spec: \"3.0\"\n", goodLogicParameter, legacyParameterTable)
	require.NoError(t, err)
	assert.Equal(t, BknSpecV3, net.BknSpec)

	at := net.ActionTypes[0]
	require.NotNil(t, at.ActionSource)
	assert.Equal(t, "tool_restart", at.ActionSource.ToolID)
	assert.Equal(t, "重启异常 Pod。", at.Description)

	require.Len(t, at.Parameters, 2)
	assert.Equal(t, Parameter{Name: "pod", Type: "string", ValueFrom: "property", Value: "pod_name", Description: "Pod 名称"}, at.Parameters[0])
	assert.Equal(t, Parameter{Name: "reason", Type: "string", ValueFrom: "input", Value: "", Description: "原因"}, at.Parameters[1])
}

// Under 3.0 a parameter that execution would silently drop is refused at import.
func TestLoadNetwork_V3RefusesParametersExecutionWouldDrop(t *testing.T) {
	spec := "bkn_spec: \"3.0\"\n"
	current := "| Name | Type | Source | Operation | ValueFrom | Value | Description |\n|------|------|--------|-----------|-----------|-------|-------------|\n"

	_, err := loadSpecNetwork(t, spec, goodLogicParameter, current+"| pod | string | property | | pod_name | | |")
	assert.ErrorContains(t, err, "belongs in ValueFrom, not Source")
	var specErr *SpecError
	assert.ErrorAs(t, err, &specErr, "importers recognize a spec refusal through the wrapping")

	_, err = loadSpecNetwork(t, spec, goodLogicParameter, current+"| pod | string | Body | | | | |")
	assert.ErrorContains(t, err, "ValueFrom must be property, input or const")

	_, err = loadSpecNetwork(t, spec, `| pod | string | property | | pod_name | | |`, current+"| pod | string | Body | | property | pod_name | |")
	assert.ErrorContains(t, err, `logic property "cpu"`)
}
