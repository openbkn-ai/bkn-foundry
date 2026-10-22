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

// A legacy parameter table read by 2.x stores the value source in Source and the property name in
// ValueFrom; the check finds it and suggests the rewrite 3.0 shows.
func TestFindMisplacedParameters_InALegacyTableReadBy2x(t *testing.T) {
	net, err := loadV3(t,
		v3Edit{fNet, "bkn_spec: \"3.0\"\n", ""},
		v3Edit{fAct, "| Name | Type | Source | Operation | ValueFrom | Value | Description |\n|------|------|--------|-----------|-----------|-------|-------------|\n" +
			"| pod | string | Body | | property | pod_name | Pod 名称 |\n| reason | string | Body | | input | | 原因 |\n",
			"| Parameter | Type | Source | Binding | Description |\n|-----------|------|--------|---------|-------------|\n" +
				"| pod | string | property | pod_name | Pod 名称 |\n| reason | string | input | - | 原因 |\n"},
	)
	require.NoError(t, err)

	found := FindMisplacedParameters(net)
	require.Len(t, found, 2)
	assert.Equal(t, "restart_pod", found[0].ActionTypeID)
	assert.Equal(t, "pod", found[0].Parameter)
	assert.Equal(t, "property", found[0].Current.Source)
	assert.Equal(t, "pod_name", found[0].Current.ValueFrom)
	assert.Equal(t, Parameter{Name: "pod", Type: "string", ValueFrom: "property", Value: "pod_name", Description: "Pod 名称"}, found[0].Suggested)
	assert.False(t, found[0].Conflict)
	assert.Equal(t, Parameter{Name: "reason", Type: "string", ValueFrom: "input", Value: "", Description: "原因"}, found[1].Suggested)
}

func TestFindMisplacedParameters(t *testing.T) {
	param := func(source, valueFrom string, value any) Parameter {
		return Parameter{Name: "p", Type: "string", Source: source, ValueFrom: valueFrom, Value: value}
	}
	net := &BknNetwork{
		ActionTypes: []*BknActionType{
			{BknActionTypeFrontmatter: BknActionTypeFrontmatter{ID: "b"}, Parameters: []Parameter{
				param(" Property ", "pod_name", nil),
				param("Body", "property", "pod_name"), // the current layout is not misplaced
			}},
			{BknActionTypeFrontmatter: BknActionTypeFrontmatter{ID: "a"}, Parameters: []Parameter{
				param("input", "any text", nil),
				param("const", "-", nil),
				param("property", "pod_name", "already set"),
			}},
		},
		ObjectTypes: []*BknObjectType{{
			BknObjectTypeFrontmatter: BknObjectTypeFrontmatter{ID: "pod"},
			LogicProperties:          []*LogicProperty{{Name: "cpu", Parameters: []Parameter{param("PROPERTY", "pod_name", nil)}}},
		}},
	}
	found := FindMisplacedParameters(net)
	require.Len(t, found, 5)

	var where []string
	for _, m := range found {
		where = append(where, m.ActionTypeID+m.ObjectTypeID+"/"+m.LogicProperty)
	}
	assert.Equal(t, []string{"a/", "a/", "a/", "b/", "pod/cpu"}, where, "action types first, each by id")

	assert.Equal(t, "input", found[0].Suggested.ValueFrom)
	assert.Equal(t, "", found[0].Suggested.Value, "an input row takes no value, whatever Binding held")
	assert.Equal(t, "const", found[1].Suggested.ValueFrom)
	assert.Equal(t, "", found[1].Suggested.Value, "- in Binding is no value")
	assert.True(t, found[2].Conflict, "a Value already set would be replaced")
	assert.Equal(t, "property", found[3].Suggested.ValueFrom, "Source is compared trimmed and in any case")
	assert.Equal(t, "pod_name", found[3].Suggested.Value)
	assert.Equal(t, "property", found[4].Suggested.ValueFrom)
	assert.Equal(t, "", found[4].Suggested.Source, "the rewrite leaves Source empty, as the legacy table had no position")
}

// Every parameter a 3.0 import refuses is reported once: misplaced with a rewrite, or invalid without
// one. A ValueFrom that already holds a value source is accepted, whatever Source says.
func TestFindInvalidParameters(t *testing.T) {
	param := func(name, source, valueFrom string) Parameter {
		return Parameter{Name: name, Source: source, ValueFrom: valueFrom}
	}
	net := &BknNetwork{ActionTypes: []*BknActionType{{
		BknActionTypeFrontmatter: BknActionTypeFrontmatter{ID: "a"},
		Parameters: []Parameter{
			param("empty", "Body", ""),
			param("name", "", "pod_name"),
			param("misplaced", "property", "pod_name"),
			param("fine", "Body", "input"),
			param("odd but accepted", "property", "const"),
		},
	}}}
	invalid := FindInvalidParameters(net)
	require.Len(t, invalid, 2)
	assert.Equal(t, "empty", invalid[0].Parameter)
	assert.Equal(t, "a", invalid[0].ActionTypeID)
	assert.Equal(t, "name", invalid[1].Parameter)

	misplaced := FindMisplacedParameters(net)
	require.Len(t, misplaced, 1)
	assert.Equal(t, "misplaced", misplaced[0].Parameter)
}

func TestHasV3Declarations(t *testing.T) {
	plain := func() *BknNetwork {
		return &BknNetwork{
			ObjectTypes: []*BknObjectType{{
				PrimaryKeys:    []string{"id"},
				DataProperties: []*DataProperty{{Name: "id", Required: true}, {Name: "name"}},
			}},
			ActionTypes:   []*BknActionType{{}},
			RelationTypes: []*BknRelationType{{}},
		}
	}
	assert.False(t, HasV3Declarations(plain()), "Required on a primary key is implied")

	for name, declare := range map[string]func(*BknNetwork){
		"Semantic":          func(n *BknNetwork) { n.ObjectTypes[0].DataProperties[1].Semantic = SemanticStatus },
		"Required":          func(n *BknNetwork) { n.ObjectTypes[0].DataProperties[1].Required = true },
		"constraints":       func(n *BknNetwork) { n.ObjectTypes[0].DataProperties[1].Constraints = &PropertyConstraints{Unit: "kg"} },
		"property aliases":  func(n *BknNetwork) { n.ObjectTypes[0].DataProperties[1].Aliases = []string{"名字"} },
		"object lifecycle":  func(n *BknNetwork) { n.ObjectTypes[0].Lifecycle = &Lifecycle{Status: LifecycleDeprecated} },
		"action aliases":    func(n *BknNetwork) { n.ActionTypes[0].Aliases = []string{"重启"} },
		"Cardinality":       func(n *BknNetwork) { n.RelationTypes[0].Endpoint.Cardinality = CardinalityManyToOne },
		"relation property": func(n *BknNetwork) { n.RelationTypes[0].DataProperties = []*DataProperty{{Name: "x"}} },
		"value type":        func(n *BknNetwork) { n.ValueTypes = []*BknValueType{{}} },
	} {
		net := plain()
		declare(net)
		assert.True(t, HasV3Declarations(net), name)
	}
}
