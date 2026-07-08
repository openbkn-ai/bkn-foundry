// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// === Parse Frontmatter Tests ===

func TestParseFrontmatter_Success(t *testing.T) {
	text := `---
type: object_type
id: pod
name: Pod
---

## ObjectType: pod
Content here
`
	fm, err := ParseFrontmatter(text)
	require.NoError(t, err)
	assert.Equal(t, "object_type", fm["type"])
	assert.Equal(t, "pod", fm["id"])
	assert.Equal(t, "Pod", fm["name"])
}

func TestParseFrontmatter_NoFrontmatter(t *testing.T) {
	text := "# No frontmatter\nJust content"
	fm, err := ParseFrontmatter(text)
	// When there's no frontmatter, it returns empty map without error
	require.NoError(t, err)
	assert.Empty(t, fm)
}

func TestParseFrontmatter_EmptyFrontmatter(t *testing.T) {
	text := `---
---

Content`
	fm, err := ParseFrontmatter(text)
	require.NoError(t, err)
	assert.Empty(t, fm)
}

// === Parse Network File Tests ===

func TestParseNetworkFile_Success(t *testing.T) {
	text := `---
type: network
id: k8s-network
name: Kubernetes Network
version: "1.0"
---

## Network: k8s-network

Kubernetes resource network
`
	net, err := ParseNetworkFile(text, "/test/network.bkn")
	require.NoError(t, err)
	assert.Equal(t, "network", net.Type)
	assert.Equal(t, "k8s-network", net.ID)
	assert.Equal(t, "Kubernetes Network", net.Name)
	assert.Equal(t, "1.0", net.Version)
}

func TestParseNetworkFile_MissingType(t *testing.T) {
	text := `---
id: test
---

Content`
	_, err := ParseNetworkFile(text, "/test/network.bkn")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "type")
}

func TestParseNetworkFile_MissingID(t *testing.T) {
	text := `---
type: network
---

Content`
	_, err := ParseNetworkFile(text, "/test/network.bkn")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "id")
}

// === Parse Object Type Tests ===

func TestParseObjectType_Basic(t *testing.T) {
	text := `---
type: object_type
id: pod
name: Pod
tags: [k8s, workload]
---

## ObjectType: pod

Kubernetes Pod resource

### Data Properties

| Name | DisplayName | Type | Description |
|------|-------------|------|-------------|
| name | Name | string | Pod name |
| image | Image | string | Container image |
`
	ot, err := ParseObjectTypeFile(text, "/test/pod.bkn")
	require.NoError(t, err)
	assert.Equal(t, "pod", ot.ID)
	assert.Equal(t, "Pod", ot.Name)
	assert.ElementsMatch(t, []string{"k8s", "workload"}, ot.Tags)
	require.Len(t, ot.DataProperties, 2)
	assert.Equal(t, "name", ot.DataProperties[0].Name)
}

func TestParseObjectType_WithDataSource(t *testing.T) {
	text := `---
type: object_type
id: deployment
name: Deployment
---

## ObjectType: deployment

### Data Source

| Type | ID | Name |
|------|-----|------|
| data_view | dv_deployments | Deployment View |

### Data Properties

| Name | DisplayName | Type |
|------|-------------|------|
| replicas | Replicas | number |
`
	ot, err := ParseObjectTypeFile(text, "/test/deployment.bkn")
	require.NoError(t, err)
	require.NotNil(t, ot.DataSource)
	assert.Equal(t, "data_view", ot.DataSource.Type)
	assert.Equal(t, "dv_deployments", ot.DataSource.ID)
	assert.Equal(t, "Deployment View", ot.DataSource.Name)
}

func TestParseObjectType_WithLogicProperties(t *testing.T) {
	text := `---
type: object_type
id: service
name: Service
---

## ObjectType: service

### Logic Properties

| Name | DisplayName | Type | Description |
|------|-------------|------|-------------|
| endpoint_count | Endpoint Count | integer | Number of endpoints |
| health_status | Health Status | string | Health status |
`
	ot, err := ParseObjectTypeFile(text, "/test/service.bkn")
	require.NoError(t, err)
	// Logic properties are parsed from the table
	require.NotEmpty(t, ot.LogicProperties)
}

// === Parse Relation Type Tests ===

func TestParseRelationType_Basic(t *testing.T) {
	text := `---
type: relation_type
id: belongs_to
name: Belongs To
---

## RelationType: belongs_to

Pod belongs to Node

### Endpoint

| Source | Target | Type |
|--------|--------|------|
| pod | node | direct |

### Mapping Rules

| Source Property | Target Property |
|-----------------|-----------------|
| node_name | name |
| node_id | id |
`
	rt, err := ParseRelationTypeFile(text, "/test/belongs_to.bkn")
	require.NoError(t, err)
	assert.Equal(t, "belongs_to", rt.ID)
	assert.Equal(t, "Pod belongs to Node", rt.Description)
	assert.Equal(t, "pod", rt.Endpoint.Source)
	assert.Equal(t, "node", rt.Endpoint.Target)
	assert.Equal(t, "direct", rt.Endpoint.Type)
	rules, ok := rt.MappingRules.(DirectMappingRule)
	require.True(t, ok, "MappingRules should be DirectMappingRule")
	require.Len(t, rules, 2)
	assert.Equal(t, "node_name", rules[0].SourceProperty)
}

func TestParseRelationType_EndpointTable(t *testing.T) {
	text := `---
type: relation_type
id: runs_on
name: Runs On
---

## RelationType: runs_on

Container runs on Pod

### Endpoint

| Source | Target | Type |
|--------|--------|------|
| container | pod | direct |

### Mapping Rules

| Source Property | Target Property |
|-----------------|-----------------|
| pod_id | id |
`
	rt, err := ParseRelationTypeFile(text, "/test/runs_on.bkn")
	require.NoError(t, err)
	assert.Equal(t, "container", rt.Endpoint.Source)
	assert.Equal(t, "pod", rt.Endpoint.Target)
	assert.Equal(t, "direct", rt.Endpoint.Type)
	rules, ok := rt.MappingRules.(DirectMappingRule)
	require.True(t, ok, "MappingRules should be DirectMappingRule")
	require.Len(t, rules, 1)
}

// === Parse Action Type Tests ===

func TestParseActionType_Basic(t *testing.T) {
	text := `---
type: action_type
id: restart
name: Restart Pod
action_type: modify
risk_level: high
requires_approval: true
---

## ActionType: restart

Restart a pod gracefully

### Bound Object

| Bound Object |
|--------------|
| pod |

### Parameter Binding

| Name | Type | Source | Operation | ValueFrom | Value | Description |
|------|------|--------|-----------|-----------|-------|-------------|
| graceful | boolean | const | | | true | Graceful restart |
| timeout | number | property | | spec.timeout | | Timeout seconds |
`
	at, err := ParseActionTypeFile(text, "/test/restart.bkn")
	require.NoError(t, err)
	assert.Equal(t, "restart", at.ID)
	assert.Equal(t, "pod", at.BoundObject)
	assert.Equal(t, "modify", at.ActionType)
	require.Len(t, at.Parameters, 2)
	assert.Equal(t, "graceful", at.Parameters[0].Name)
}

func TestParseActionType_WithSchedule(t *testing.T) {
	text := `---
type: action_type
id: backup
name: Backup Data
---

## ActionType: backup

### Schedule

| Type | Expression |
|------|------------|
| cron | 0 2 * * * |
`
	at, err := ParseActionTypeFile(text, "/test/backup.bkn")
	require.NoError(t, err)
	require.NotNil(t, at.Schedule)
	assert.Equal(t, "cron", at.Schedule.Type)
	assert.Equal(t, "0 2 * * *", at.Schedule.Expression)
}

// === Parse Risk Type Tests ===

func TestParseRiskType_Basic(t *testing.T) {
	text := `---
type: risk_type
id: high_memory
name: High Memory Usage
---

## RiskType: high_memory

Detects high memory usage

### Control Scope

production

### Pre-checks

| Object | Check | Condition | Message |
|--------|-------|-----------|---------|
| pod | memory_check | memory > 90 | Memory usage too high |
| node | swap_check | swap > 50 | Swap usage too high |
`
	rt, err := ParseRiskTypeFile(text, "/test/high_memory.bkn")
	require.NoError(t, err)
	assert.Equal(t, "high_memory", rt.ID)
	assert.Equal(t, "High Memory Usage", rt.Name)
}

// === Parse Concept Group Tests ===

func TestParseConceptGroup_Basic(t *testing.T) {
	text := `---
type: concept_group
id: k8s_resources
name: Kubernetes Resources
---

## ConceptGroup: k8s_resources

Core Kubernetes resources
`
	cg, err := ParseConceptGroupFile(text, "/test/k8s_resources.bkn")
	require.NoError(t, err)
	assert.Equal(t, "k8s_resources", cg.ID)
	assert.Equal(t, "Kubernetes Resources", cg.Name)
}

// === Error Handling Tests ===

func TestParse_InvalidType(t *testing.T) {
	text := `---
type: invalid_type
id: test
---

Content`
	// Parser does not validate the type field — that happens at a higher level.
	ot, err := ParseObjectTypeFile(text, "/test/invalid.bkn")
	require.NoError(t, err)
	assert.Equal(t, "test", ot.ID)
}

func TestParse_MalformedYAML(t *testing.T) {
	text := `---
type: object_type
id: [invalid yaml structure
---

Content`
	_, err := ParseFrontmatter(text)
	assert.Error(t, err)
}

func TestParse_EmptyFile(t *testing.T) {
	fm, err := ParseFrontmatter("")
	// Empty file returns empty frontmatter without error
	require.NoError(t, err)
	assert.Empty(t, fm)
}

// === Data Properties Parsing Tests ===

func TestParseDataProperties_VariousTypes(t *testing.T) {
	text := `---
type: object_type
id: test
---

## ObjectType: test

### Data Properties

| Name | DisplayName | Type | Description |
|------|-------------|------|-------------|
| str_field | String Field | string | A string |
| num_field | Number Field | number | A number |
| bool_field | Bool Field | boolean | A boolean |
| date_field | Date Field | datetime | A date |
| json_field | JSON Field | json | JSON data |
`
	ot, err := ParseObjectTypeFile(text, "/test/test.bkn")
	require.NoError(t, err)
	require.Len(t, ot.DataProperties, 5)
	assert.Equal(t, "string", ot.DataProperties[0].Type)
	assert.Equal(t, "number", ot.DataProperties[1].Type)
	assert.Equal(t, "boolean", ot.DataProperties[2].Type)
	assert.Equal(t, "datetime", ot.DataProperties[3].Type)
	assert.Equal(t, "json", ot.DataProperties[4].Type)
}

func TestParseDataProperties_WithMappedField(t *testing.T) {
	text := `---
type: object_type
id: test
---

## ObjectType: test

### Data Properties

| Name | Display Name | Type | Description | Mapped Field |
|------|--------------|------|-------------|--------------|
| status | Status | string | Status field | status_code |
| name | Name | string | Name field | full_name |
`
	ot, err := ParseObjectTypeFile(text, "/test/test.bkn")
	require.NoError(t, err)
	require.Len(t, ot.DataProperties, 2)
	assert.Equal(t, "status", ot.DataProperties[0].Name)
	assert.Equal(t, "Status field", ot.DataProperties[0].Description)
	assert.Equal(t, "status_code", ot.DataProperties[0].MappedField)
	assert.Equal(t, "name", ot.DataProperties[1].Name)
	assert.Equal(t, "full_name", ot.DataProperties[1].MappedField)
}

// === Logic Properties Parsing Tests ===

func TestParseLogicProperties_WithParameters(t *testing.T) {
	text := `---
type: object_type
id: test
---

## ObjectType: test

### Logic Properties

| Name | DisplayName | Type | Description |
|------|-------------|------|-------------|
| computed_value | Computed Value | number | A computed value |
`
	ot, err := ParseObjectTypeFile(text, "/test/test.bkn")
	require.NoError(t, err)
	// Logic properties are parsed from the table
	require.NotEmpty(t, ot.LogicProperties)
}

func TestParseLogicProperties_SubSection(t *testing.T) {
	text := `---
type: object_type
id: product
name: 产品
---

## ObjectType: 产品

存储企业生产的成品基本信息

### Logic Properties

#### product_bom

**Meta**

| Display Name | Type | Description |
|--------------|------|-------------|
| product_bom | operator |  |

**Source**

| Source Type | Source ID | Source Name |
|-------------|-----------|-------------|
| operator | bom_tree_builder | bom_tree_builder |

**Parameters**

| Name | Type | Source | Operation | ValueFrom | Value | Description |
|------|------|--------|-----------|-----------|-------|-------------|
| timeout | number | input |  |  |  |  |
| cache | boolean | input |  |  |  |  |
| knowledge_network_id | string | const |  | supplychain_hd0202 |  |  |

**Analysis Dimensions**

| Name | Display Name | Type | Description |
|------|--------------|------|-------------|
`
	ot, err := ParseObjectTypeFile(text, "/test/product.bkn")
	require.NoError(t, err)
	assert.Equal(t, "存储企业生产的成品基本信息", ot.Description)
	require.Len(t, ot.LogicProperties, 1)

	lp := ot.LogicProperties[0]
	assert.Equal(t, "product_bom", lp.Name)
	assert.Equal(t, "product_bom", lp.DisplayName)
	assert.Equal(t, "operator", lp.Type)
	require.NotNil(t, lp.DataSource)
	assert.Equal(t, "bom_tree_builder", lp.DataSource.ID)
	assert.Equal(t, "operator", lp.DataSource.Type)
	require.Len(t, lp.Parameters, 3)
	assert.Equal(t, "timeout", lp.Parameters[0].Name)
	assert.Equal(t, "const", lp.Parameters[2].Source)
	assert.Equal(t, "supplychain_hd0202", lp.Parameters[2].ValueFrom)
}

func TestParseLogicProperties_Empty(t *testing.T) {
	text := `---
type: object_type
id: material
name: 物料
---

## ObjectType: 物料

物料基础信息

### Logic Properties


### Keys

Primary Keys: material_code
`
	ot, err := ParseObjectTypeFile(text, "/test/material.bkn")
	require.NoError(t, err)
	assert.Empty(t, ot.LogicProperties)
}

// === Parameter Binding Tests ===

func TestParseParameters_VariousSources(t *testing.T) {
	text := `---
type: action_type
id: test_action
---

## ActionType: test_action

### Parameter Binding

| Name | Type | Source | Operation | ValueFrom | Value | Description |
|------|------|--------|-----------|-----------|-------|-------------|
| fixed_val | string | const | | | hello | Fixed value |
| from_prop | string | property | | metadata.name | | From property |
`
	at, err := ParseActionTypeFile(text, "/test/test_action.bkn")
	require.NoError(t, err)
	require.Len(t, at.Parameters, 2)
	assert.Equal(t, "const", at.Parameters[0].Source)
	assert.Equal(t, "hello", at.Parameters[0].Value)
	assert.Equal(t, "property", at.Parameters[1].Source)
	assert.Equal(t, "metadata.name", at.Parameters[1].ValueFrom)
}

// === Edge Cases Tests ===

func TestParse_EmptyTables(t *testing.T) {
	text := `---
type: object_type
id: empty_obj
---

## ObjectType: empty_obj

No tables here
`
	ot, err := ParseObjectTypeFile(text, "/test/empty.bkn")
	require.NoError(t, err)
	assert.Empty(t, ot.DataProperties)
	assert.Empty(t, ot.LogicProperties)
}

func TestParse_ExtraWhitespace(t *testing.T) {
	text := `---
type: object_type
id: test
name: Test
---

## ObjectType: test

   

### Data Properties

| Name | DisplayName | Type |
|------|-------------|------|
| field1 | Field 1 | string |

   
`
	ot, err := ParseObjectTypeFile(text, "/test/test.bkn")
	require.NoError(t, err)
	assert.Equal(t, "test", ot.ID)
	require.Len(t, ot.DataProperties, 1)
}

func TestParse_SpecialCharactersInID(t *testing.T) {
	text := `---
type: object_type
id: my-app_v1.0
name: My App
---

## ObjectType: my-app_v1.0

Content
`
	ot, err := ParseObjectTypeFile(text, "/test/my-app_v1.0.bkn")
	require.NoError(t, err)
	assert.Equal(t, "my-app_v1.0", ot.ID)
}

func TestParse_UnicodeContent(t *testing.T) {
	text := `---
type: object_type
id: unicode_test
name: 测试对象
---

## ObjectType: 测试对象

这是一个测试对象

### Data Properties

| Name | DisplayName | Type |
|------|-------------|------|
| 名称 | 名称 | string |
`
	ot, err := ParseObjectTypeFile(text, "/test/unicode.bkn")
	require.NoError(t, err)
	assert.Equal(t, "测试对象", ot.Name)
	assert.Equal(t, "这是一个测试对象", ot.Description)
	require.Len(t, ot.DataProperties, 1)
	assert.Equal(t, "名称", ot.DataProperties[0].Name)
}

// === Integration Tests ===

func TestParseFullNetwork(t *testing.T) {
	// Create a temporary directory with full network structure
	dir, err := os.MkdirTemp("", "bkn-full-network-*")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(dir) }()

	// Create network.bkn
	networkContent := `---
type: network
id: test-network
name: Test Network
version: "1.0.0"
---

## Network: test-network

Test network description
`
	err = os.WriteFile(filepath.Join(dir, "network.bkn"), []byte(networkContent), 0644)
	require.NoError(t, err)

	// Create object_types directory and file
	objTypesDir := filepath.Join(dir, "object_types")
	err = os.MkdirAll(objTypesDir, 0755)
	require.NoError(t, err)

	objContent := `---
type: object_type
id: test_obj
name: Test Object
---

## ObjectType: test_obj

Test object

### Data Properties

| Name | DisplayName | Type |
|------|-------------|------|
| id | ID | string |
`
	err = os.WriteFile(filepath.Join(objTypesDir, "test_obj.bkn"), []byte(objContent), 0644)
	require.NoError(t, err)

	// Load the network
	net, err := LoadNetwork(dir)
	require.NoError(t, err)

	assert.Equal(t, "test-network", net.ID)
	assert.Equal(t, "Test Network", net.Name)
	require.Len(t, net.ObjectTypes, 1)
	assert.Equal(t, "test_obj", net.ObjectTypes[0].ID)
}

func TestParse_InvalidFilePath(t *testing.T) {
	// Empty file path should not prevent parsing — path is only used for error context.
	ot, err := ParseObjectTypeFile("---\ntype: object_type\nid: test\n---\n", "")
	require.NoError(t, err)
	assert.Equal(t, "test", ot.ID)
}

func TestParse_NonExistentType(t *testing.T) {
	text := `---
type: network
id: test
---

Content`
	// ParseNetworkFile validates that type is "network"
	_, err := ParseNetworkFile(text, "/test/test.bkn")
	// Currently it accepts the file as long as frontmatter is valid
	// The type validation happens at higher level
	require.NoError(t, err)
}

// === TriggerCondition Tests ===

func TestParseTriggerCondition_Basic(t *testing.T) {
	text := `---
type: action_type
id: tc_basic
name: TC Basic
action_type: add
---

## ActionType: TC Basic

### Trigger Condition

` + "```yaml" + `
object_type_id: relation
field: position
operation: ==
sub_conds: []
value_from: ""
value: "1"
` + "```" + `
`
	at, err := ParseActionTypeFile(text, "/test/tc_basic.bkn")
	require.NoError(t, err)
	require.NotNil(t, at.TriggerCondition)
	assert.Equal(t, "relation", at.TriggerCondition.ObjectTypeID)
	assert.Equal(t, "position", at.TriggerCondition.Field)
	assert.Equal(t, "==", at.TriggerCondition.Operation)
	assert.Equal(t, "1", at.TriggerCondition.Value)
	assert.Empty(t, at.TriggerCondition.SubConds)
}

func TestParseTriggerCondition_WithSubConds(t *testing.T) {
	text := `---
type: action_type
id: tc_nested
name: TC Nested
action_type: modify
---

## ActionType: TC Nested

### Trigger Condition

` + "```yaml" + `
object_type_id: pod
field: ""
operation: and
sub_conds:
  - object_type_id: pod
    field: status
    operation: "=="
    sub_conds: []
    value_from: ""
    value: running
  - object_type_id: pod
    field: replicas
    operation: ">"
    sub_conds: []
    value_from: ""
    value: "0"
value_from: ""
value: ""
` + "```" + `
`
	at, err := ParseActionTypeFile(text, "/test/tc_nested.bkn")
	require.NoError(t, err)
	require.NotNil(t, at.TriggerCondition)
	assert.Equal(t, "and", at.TriggerCondition.Operation)
	require.Len(t, at.TriggerCondition.SubConds, 2)
	assert.Equal(t, "status", at.TriggerCondition.SubConds[0].Field)
	assert.Equal(t, "replicas", at.TriggerCondition.SubConds[1].Field)
}

func TestParseTriggerCondition_NoBlock(t *testing.T) {
	text := `---
type: action_type
id: tc_empty
name: TC Empty
action_type: add
---

## ActionType: TC Empty

### Trigger Condition

`
	at, err := ParseActionTypeFile(text, "/test/tc_empty.bkn")
	require.NoError(t, err)
	assert.Nil(t, at.TriggerCondition)
}

func TestParseActionType_RoundTrip(t *testing.T) {
	text := `---
type: action_type
id: 222
name: 222
tags: []
action_type: add
---

## ActionType: 222

### Bound Object

| Bound Object |
|--------------|
| relation |

### Affect Object

| Affect Object | Affect Description |
|---------------|---------------------|

### Trigger Condition

` + "```yaml" + `
object_type_id: relation
field: position
operation: ==
sub_conds: []
value_from: ""
value: "1"
` + "```" + `

### Action Source

| Type | BoxID | ToolID | McpID | ToolName |
|------|-------|--------|-------|----------|

### Parameter Binding

| Name | Type | Source | Operation | ValueFrom | Value | Description |
|------|------|--------|-----------|-----------|-------|-------------|

### Schedule

| Type | Expression |
|------|------------|

`
	// First parse
	at1, err := ParseActionTypeFile(text, "/test/222.bkn")
	require.NoError(t, err)
	require.NotNil(t, at1.TriggerCondition)

	// Serialize back to text
	serialized := SerializeActionType(at1)

	// Second parse from serialized output
	at2, err := ParseActionTypeFile(serialized, "/test/222.bkn")
	require.NoError(t, err)

	// TriggerCondition must survive the round-trip
	require.NotNil(t, at2.TriggerCondition)
	assert.Equal(t, at1.TriggerCondition.ObjectTypeID, at2.TriggerCondition.ObjectTypeID)
	assert.Equal(t, at1.TriggerCondition.Field, at2.TriggerCondition.Field)
	assert.Equal(t, at1.TriggerCondition.Operation, at2.TriggerCondition.Operation)
	assert.Equal(t, at1.TriggerCondition.Value, at2.TriggerCondition.Value)
	assert.Equal(t, at1.BoundObject, at2.BoundObject)
	assert.Equal(t, at1.ActionType, at2.ActionType)
	assert.Equal(t, at1.ActionIntent, at2.ActionIntent)
}

func TestParseActionType_ActionIntentAndImpactContracts(t *testing.T) {
	text := `---
type: action_type
id: act_impact
name: Test Impact
tags: []
action_intent: modify
---

## ActionType: Test Impact

### Bound Object

| Bound Object | Action Type |
|--------------|-------------|
| pod | modify |

### Impact Contracts

` + "```yaml\nimpact_contracts:\n  - object_type_id: pod\n    expected_operation: modify\n    description: restart workload\n    affected_fields: []\n```\n" + `

### Parameter Binding

| Name | Type | Source | Operation | ValueFrom | Value | Description |
|------|------|--------|-----------|-----------|-------|-------------|
`
	at, err := ParseActionTypeFile(text, "/test/act.bkn")
	require.NoError(t, err)
	assert.Equal(t, "modify", at.ActionIntent)
	assert.Equal(t, "modify", at.ActionType)
	require.Len(t, at.ImpactContracts, 1)
	assert.Equal(t, "pod", at.ImpactContracts[0].ObjectTypeID)
	assert.Equal(t, "modify", at.ImpactContracts[0].ExpectedOperation)
	assert.Equal(t, "restart workload", at.ImpactContracts[0].Description)
}

// === ActionType Additional Scenarios ===

func TestParseActionType_WithAffectObject(t *testing.T) {
	text := `---
type: action_type
id: scale
name: Scale
action_type: modify
---

## ActionType: Scale

### Bound Object

| Bound Object |
|--------------|
| deployment |

### Affect Object

| Affect Object | Affect Description |
|---------------|---------------------|
| pod | Pods are recreated |

### Parameter Binding

| Name | Type | Source | Operation | ValueFrom | Value | Description |
|------|------|--------|-----------|-----------|-------|-------------|
`
	at, err := ParseActionTypeFile(text, "/test/scale.bkn")
	require.NoError(t, err)
	require.NotNil(t, at.AffectObject)
	assert.Equal(t, "pod", at.AffectObject.ObjectType)
	assert.Equal(t, "Pods are recreated", at.AffectObject.Description)
}

func TestParseActionType_WithActionSource(t *testing.T) {
	text := `---
type: action_type
id: restart
name: Restart
action_type: modify
---

## ActionType: Restart

### Action Source

| Type | BoxID | ToolID | McpID | ToolName |
|------|-------|--------|-------|----------|
| tool | box-001 | tool-abc | | |

### Parameter Binding

| Name | Type | Source | Operation | ValueFrom | Value | Description |
|------|------|--------|-----------|-----------|-------|-------------|
`
	at, err := ParseActionTypeFile(text, "/test/restart.bkn")
	require.NoError(t, err)
	require.NotNil(t, at.ActionSource)
	assert.Equal(t, "tool", at.ActionSource.Type)
	assert.Equal(t, "box-001", at.ActionSource.BoxID)
	assert.Equal(t, "tool-abc", at.ActionSource.ToolID)
}

// === RelationType data_view Tests ===

func TestParseRelationType_DataView(t *testing.T) {
	text := `---
type: relation_type
id: pod_node
name: Pod Node
---

## RelationType: Pod Node

Pod runs on Node via data view

### Endpoint

| Source | Target | Type |
|--------|--------|------|
| pod | node | data_view |

### Mapping View

| Type | ID |
|------|-----|
| data_view | view_pod_node |

### Source Mapping

| Source Property | View Property |
|-----------------|---------------|
| node_name | view_node_name |

### Target Mapping

| View Property | Target Property |
|---------------|-----------------|
| view_node_name | name |
`
	rt, err := ParseRelationTypeFile(text, "/test/pod_node.bkn")
	require.NoError(t, err)
	assert.Equal(t, "data_view", rt.Endpoint.Type)

	rules, ok := rt.MappingRules.(*InDirectMappingRule)
	require.True(t, ok, "MappingRules should be InDirectMappingRule for data_view")
	require.NotNil(t, rules.BackingDataSource)
	assert.Equal(t, "data_view", rules.BackingDataSource.Type)
	assert.Equal(t, "view_pod_node", rules.BackingDataSource.ID)
	require.Len(t, rules.SourceMappingRules, 1)
	assert.Equal(t, "node_name", rules.SourceMappingRules[0].SourceProperty)
	assert.Equal(t, "view_node_name", rules.SourceMappingRules[0].TargetProperty)
	require.Len(t, rules.TargetMappingRules, 1)
	assert.Equal(t, "view_node_name", rules.TargetMappingRules[0].SourceProperty)
	assert.Equal(t, "name", rules.TargetMappingRules[0].TargetProperty)
}

// === RelationType filtered_cross_join Tests ===

func TestParseRelationType_FilteredCrossJoin(t *testing.T) {
	text := `---
type: relation_type
id: emp_dept
name: Emp Dept
---

## RelationType: Emp Dept

### Endpoint

| Source | Target | Type |
|--------|--------|------|
| employee | department | filtered_cross_join |

### Source Condition

` + "```yaml" + `
field: status
operation: eq
value: active
` + "```" + `

### Target Condition

` + "```yaml" + `
field: active
operation: eq
value: true
` + "```" + `
`
	rt, err := ParseRelationTypeFile(text, "/test/emp_dept.bkn")
	require.NoError(t, err)
	assert.Equal(t, "filtered_cross_join", rt.Endpoint.Type)

	rules, ok := rt.MappingRules.(*FilteredCrossJoinMapping)
	require.True(t, ok, "MappingRules should be *FilteredCrossJoinMapping for filtered_cross_join")

	require.NotNil(t, rules.SourceCondition)
	assert.Equal(t, "status", rules.SourceCondition.Field)
	assert.Equal(t, "eq", rules.SourceCondition.Operation)

	require.NotNil(t, rules.TargetCondition)
	assert.Equal(t, "active", rules.TargetCondition.Field)
	assert.Equal(t, "eq", rules.TargetCondition.Operation)
}

func TestParseRelationType_FilteredCrossJoin_NilConditions(t *testing.T) {
	text := `---
type: relation_type
id: emp_dept
name: Emp Dept
---

## RelationType: Emp Dept

### Endpoint

| Source | Target | Type |
|--------|--------|------|
| employee | department | filtered_cross_join |

### Source Condition

### Target Condition

`
	rt, err := ParseRelationTypeFile(text, "/test/emp_dept.bkn")
	require.NoError(t, err)
	assert.Equal(t, "filtered_cross_join", rt.Endpoint.Type)

	rules, ok := rt.MappingRules.(*FilteredCrossJoinMapping)
	require.True(t, ok)
	assert.Nil(t, rules.SourceCondition)
	assert.Nil(t, rules.TargetCondition)
}

func TestParseMetricFile_MockEmployeeOnboarded(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	path := filepath.Join(filepath.Dir(file), "..", "..", "..", "examples", "mock_system", "metrics", "mock_employee_onboarded_count.bkn")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	m, err := ParseMetricFile(string(data), path)
	require.NoError(t, err)
	assert.Equal(t, "metric", m.Type)
	assert.Equal(t, "mock_employee_onboarded_count", m.ID)
	assert.Equal(t, "object_type", m.ScopeType)
	assert.Equal(t, "employee", m.ScopeRef)
	require.True(t, m.HasScopeSection)
	require.True(t, m.HasCalculationFormulaSection)
	require.True(t, m.HasMetricAttributesSection)
	require.NotNil(t, m.Formula)
	assert.Equal(t, "atomic", m.Formula.Kind)
	assert.Equal(t, "atomic", m.MetricAttributes.MetricType)
	require.NotNil(t, m.Formula.Atomic)
	require.NotNil(t, m.Formula.Atomic.Condition)
	assert.Equal(t, "status", m.Formula.Atomic.Condition.Field)
	require.NotNil(t, m.Formula.Atomic.Aggregation)
	assert.Equal(t, "id", m.Formula.Atomic.Aggregation.Property)
	assert.Equal(t, "count", m.Formula.Atomic.Aggregation.Aggr)
	require.Len(t, m.TimeDimensions, 1)
	assert.Equal(t, "hire_date", m.TimeDimensions[0].Property)
	require.Len(t, m.AnalysisDimensions, 1)
	assert.Equal(t, "name", m.AnalysisDimensions[0].Name)
}

func TestParseMetricAttributes_WideTable(t *testing.T) {
	a := parseMetricAttributes(`| Metric Type | Unit Type | Unit |
|---|---|---|
| atomic | cnt | pcs |
`)
	assert.Equal(t, "atomic", a.MetricType)
	assert.Equal(t, "cnt", a.UnitType)
	assert.Equal(t, "pcs", a.Unit)
}

func TestSerializeMetric_MetricTypeFromKind(t *testing.T) {
	m := &BknMetric{
		BknMetricFrontmatter: BknMetricFrontmatter{
			Type: "metric",
			ID:   "m1",
			Name: "One",
			Tags: []string{"t"},
		},
		Description: "d",
		ScopeType:   "object_type",
		ScopeRef:    "obj1",
		Formula: &MetricFormula{
			Kind: "atomic",
			Atomic: &MetricAtomic{
				Aggregation: &MetricAggregation{Property: "id", Aggr: "count"},
			},
		},
		HasScopeSection:              true,
		HasCalculationFormulaSection: true,
	}
	out := SerializeMetric(m)
	assert.Contains(t, out, "### Metric attributes")
	assert.Contains(t, out, "| Metric Type | Unit Type | Unit |")
	assert.Contains(t, out, "| atomic |  |  |")
	assert.NotContains(t, out, "\nmetric_type: atomic\n")
}
