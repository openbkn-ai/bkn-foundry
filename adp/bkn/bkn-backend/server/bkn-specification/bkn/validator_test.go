// Copyright openbkn.ai
// Copyright The kweaver-ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateNetwork_MissingID(t *testing.T) {
	net := &BknNetwork{
		BknNetworkFrontmatter: BknNetworkFrontmatter{
			Type: "knowledge_network",
			ID:   "net",
			Name: "Net",
		},
		ObjectTypes: []*BknObjectType{
			{
				BknObjectTypeFrontmatter: BknObjectTypeFrontmatter{
					Type: "object_type",
					ID:   "",
					Name: "X",
				},
				HasDataPropertiesSection: true,
				HasKeysSection:           true,
			},
		},
	}
	res := ValidateNetwork(net)
	assert.False(t, res.OK())
	var found bool
	for _, e := range res.Errors {
		if e.Code == "missing_frontmatter_field" && e.Column == "id" {
			found = true
			break
		}
	}
	assert.True(t, found, "expected missing_frontmatter_field on id, got %+v", res.Errors)
}

func TestValidateNetwork_InvalidIDFormat(t *testing.T) {
	net := &BknNetwork{
		BknNetworkFrontmatter: BknNetworkFrontmatter{
			Type: "knowledge_network",
			ID:   "net",
			Name: "Net",
		},
		ObjectTypes: []*BknObjectType{
			{
				BknObjectTypeFrontmatter: BknObjectTypeFrontmatter{
					Type: "object_type",
					ID:   "Bad_Upper",
					Name: "Bad",
				},
				HasDataPropertiesSection: true,
				HasKeysSection:           true,
				DataProperties:           []*DataProperty{{Name: "k", Type: "string"}},
				PrimaryKeys:              []string{"k"},
				DisplayKey:               "k",
			},
		},
	}
	res := ValidateNetwork(net)
	assert.False(t, res.OK())
	var found bool
	for _, e := range res.Errors {
		if e.Code == "invalid_id" {
			found = true
			break
		}
	}
	assert.True(t, found, "expected invalid_id, got %+v", res.Errors)
}

func TestValidateNetwork_InvalidEndpointRef(t *testing.T) {
	net := &BknNetwork{
		BknNetworkFrontmatter: BknNetworkFrontmatter{
			Type: "knowledge_network",
			ID:   "net",
			Name: "Net",
		},
		ObjectTypes: []*BknObjectType{
			{
				BknObjectTypeFrontmatter: BknObjectTypeFrontmatter{
					Type: "object_type",
					ID:   "a",
					Name: "A",
				},
				HasDataPropertiesSection: true,
				HasKeysSection:           true,
			},
		},
		RelationTypes: []*BknRelationType{
			{
				BknRelationTypeFrontmatter: BknRelationTypeFrontmatter{
					Type: "relation_type",
					ID:   "r1",
					Name: "R",
				},
				Endpoint: Endpoint{
					Source: "a",
					Target: "missing_obj",
					Type:   "direct",
				},
			},
		},
	}
	res := ValidateNetwork(net)
	assert.False(t, res.OK())
	var found bool
	for _, e := range res.Errors {
		if e.Code == "invalid_endpoint_ref" {
			found = true
			break
		}
	}
	assert.True(t, found, "expected invalid_endpoint_ref, got %+v", res.Errors)
}

func TestValidateRelationTypeDeep_DataViewRequiresResourceBacking(t *testing.T) {
	valid := &BknRelationType{
		Endpoint: Endpoint{Source: "source", Target: "target", Type: RELATION_MAPPING_TYPE_INDIRECT},
		MappingRules: &InDirectMappingRule{
			BackingDataSource:  &ResourceInfo{Type: DATA_SOURCE_TYPE_RESOURCE, ID: "resource-1"},
			SourceMappingRules: []MappingRule{{SourceProperty: "source_id", TargetProperty: "resource_source_id"}},
			TargetMappingRules: []MappingRule{{SourceProperty: "resource_target_id", TargetProperty: "target_id"}},
		},
	}
	result := &ValidationResult{}
	validateRelationTypeDeep(result, "relations/indirect.bkn", valid)
	assert.Empty(t, result.Errors)

	valid.MappingRules.(*InDirectMappingRule).BackingDataSource.Type = "data_view"
	result = &ValidationResult{}
	validateRelationTypeDeep(result, "relations/indirect.bkn", valid)
	assert.NotEmpty(t, result.Errors)
	assert.Equal(t, "backing_data_source", result.Errors[0].Column)
	assert.Contains(t, result.Errors[0].Message, `must be "resource"`)
}

func TestValidateNetwork_InvalidBoundObjectRef(t *testing.T) {
	net := &BknNetwork{
		BknNetworkFrontmatter: BknNetworkFrontmatter{
			Type: "knowledge_network",
			ID:   "net",
			Name: "Net",
		},
		ObjectTypes: []*BknObjectType{
			{
				BknObjectTypeFrontmatter: BknObjectTypeFrontmatter{
					Type: "object_type",
					ID:   "only_one",
					Name: "O",
				},
				HasDataPropertiesSection: true,
				HasKeysSection:           true,
			},
		},
		ActionTypes: []*BknActionType{
			{
				BknActionTypeFrontmatter: BknActionTypeFrontmatter{
					Type: "action_type",
					ID:   "act1",
					Name: "Act",
				},
				BoundObject: "ghost",
			},
		},
	}
	res := ValidateNetwork(net)
	assert.False(t, res.OK())
	var found bool
	for _, e := range res.Errors {
		if e.Code == "invalid_bound_object_ref" {
			found = true
			break
		}
	}
	assert.True(t, found, "expected invalid_bound_object_ref, got %+v", res.Errors)
}

func TestValidateNetwork_LogicPropertyMetricOK(t *testing.T) {
	net := &BknNetwork{
		BknNetworkFrontmatter: BknNetworkFrontmatter{
			Type: "knowledge_network",
			ID:   "net",
			Name: "Net",
		},
		ObjectTypes: []*BknObjectType{
			{
				BknObjectTypeFrontmatter: BknObjectTypeFrontmatter{
					Type: "object_type",
					ID:   "x",
					Name: "X",
				},
				HasDataPropertiesSection: true,
				HasKeysSection:           true,
				DataProperties:           []*DataProperty{{Name: "k", DisplayName: "K", Type: "string"}},
				PrimaryKeys:              []string{"k"},
				DisplayKey:               "k",
				LogicProperties: []*LogicProperty{
					{
						Name:        "m1",
						DisplayName: "M",
						Type:        "metric",
						DataSource:  &ResourceInfo{Type: "metric", ID: "1", Name: "m"},
					},
				},
			},
		},
	}
	res := ValidateNetwork(net)
	assert.True(t, res.OK(), "expected valid logic metric property, errors: %+v", res.Errors)
}

// networkWithLogicSource builds a minimal valid network whose only logic property carries src.
func networkWithLogicSource(propType string, src *ResourceInfo) *BknNetwork {
	return &BknNetwork{
		BknNetworkFrontmatter: BknNetworkFrontmatter{
			Type: "knowledge_network",
			ID:   "net",
			Name: "Net",
		},
		ObjectTypes: []*BknObjectType{
			{
				BknObjectTypeFrontmatter: BknObjectTypeFrontmatter{
					Type: "object_type",
					ID:   "x",
					Name: "X",
				},
				HasDataPropertiesSection: true,
				HasKeysSection:           true,
				DataProperties:           []*DataProperty{{Name: "k", DisplayName: "K", Type: "string"}},
				PrimaryKeys:              []string{"k"},
				DisplayKey:               "k",
				LogicProperties: []*LogicProperty{
					{Name: "lp1", DisplayName: "LP1", Type: propType, DataSource: src},
				},
			},
		},
	}
}

func TestValidateNetwork_LogicPropertyToolSourceOK(t *testing.T) {
	res := ValidateNetwork(networkWithLogicSource("tool", &ResourceInfo{
		Type: "tool", Name: "coupon_stats", BoxID: "box-1", ToolID: "tool-1", ResultPath: "$.result.rate_pct",
	}))
	assert.True(t, res.OK(), "expected valid tool logic property, errors: %+v", res.Errors)
}

func TestValidateNetwork_LogicPropertyToolSourceMissingIDs(t *testing.T) {
	res := ValidateNetwork(networkWithLogicSource("tool", &ResourceInfo{
		Type: "tool", ID: "coupon_stats", Name: "coupon_stats",
	}))
	assert.False(t, res.OK())
	assert.Contains(t, fmt.Sprintf("%+v", res.Errors), "requires both box_id and tool_id")
}

func TestValidateNetwork_LogicPropertyMetricSourceMissingID(t *testing.T) {
	res := ValidateNetwork(networkWithLogicSource("metric", &ResourceInfo{Type: "metric", Name: "m"}))
	assert.False(t, res.OK())
	assert.Contains(t, fmt.Sprintf("%+v", res.Errors), "requires source id")
}

func TestValidateNetwork_LogicPropertyResultPathNeedsTool(t *testing.T) {
	res := ValidateNetwork(networkWithLogicSource("metric", &ResourceInfo{
		Type: "metric", ID: "1", Name: "m", ResultPath: "$.result",
	}))
	assert.False(t, res.OK())
	assert.Contains(t, fmt.Sprintf("%+v", res.Errors), "result_path is only valid")
}

func TestValidateNetwork_LogicPropertyResultPathMustParse(t *testing.T) {
	res := ValidateNetwork(networkWithLogicSource("tool", &ResourceInfo{
		Type: "tool", Name: "coupon_stats", BoxID: "box-1", ToolID: "tool-1", ResultPath: "$..[",
	}))
	assert.False(t, res.OK())
	assert.Contains(t, fmt.Sprintf("%+v", res.Errors), "not a valid jsonpath")
}
