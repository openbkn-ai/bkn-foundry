// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package bkn

import (
	"strings"
	"testing"

	"bkn-backend/common/maskrule"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestObjectTypeMaskRuleRoundTrip(t *testing.T) {
	keepStart, keepEnd := 3, 4
	original := &BknObjectType{
		BknObjectTypeFrontmatter: BknObjectTypeFrontmatter{Type: "object_type", ID: "customer", Name: "Customer"},
		DataProperties: []*DataProperty{{
			Name: "mobile", DisplayName: "Mobile", Type: "string", MappedField: "mobile",
			MaskRule: &maskrule.Rule{Kind: maskrule.KindPartial, KeepStart: &keepStart, KeepEnd: &keepEnd, Replacement: "|"},
		}},
		PrimaryKeys: []string{"mobile"},
		DisplayKey:  "mobile",
	}

	serialized := SerializeObjectType(original)
	assert.Contains(t, serialized, "| Mask Rule |")
	assert.Contains(t, serialized, `"replacement":"\u007c"`)

	parsed, err := ParseObjectTypeFile(serialized, "customer.bkn")
	require.NoError(t, err)
	require.Len(t, parsed.DataProperties, 1)
	assert.Equal(t, original.DataProperties[0].MaskRule, parsed.DataProperties[0].MaskRule)
}

func TestParseObjectTypeMaskRuleCompatibility(t *testing.T) {
	legacy := `---
type: object_type
id: customer
name: Customer
---

## ObjectType: Customer

### Data Properties

| Name | Display Name | Type | Description | Mapped Field |
|------|--------------|------|-------------|--------------|
| id | ID | string | identifier | id |
`
	parsed, err := ParseObjectTypeFile(legacy, "legacy.bkn")
	require.NoError(t, err)
	require.Len(t, parsed.DataProperties, 1)
	assert.Nil(t, parsed.DataProperties[0].MaskRule)
	assert.NotContains(t, SerializeObjectType(parsed), "| Mask Rule |")

	invalid := strings.Replace(legacy,
		"| Name | Display Name | Type | Description | Mapped Field |",
		"| Name | Display Name | Type | Description | Mapped Field | Mask Rule |", 1)
	invalid = strings.Replace(invalid,
		"|------|--------------|------|-------------|--------------|",
		"|------|--------------|------|-------------|--------------|-----------|", 1)
	invalid = strings.Replace(invalid, "| id | ID | string | identifier | id |", "| id | ID | string | identifier | id | {bad-json} |", 1)
	_, err = ParseObjectTypeFile(invalid, "invalid.bkn")
	assert.ErrorContains(t, err, "invalid Mask Rule JSON")
}

func TestValidateNetworkChecksMaskRule(t *testing.T) {
	keepStart, keepEnd := 1, 1
	network := &BknNetwork{
		BknNetworkFrontmatter: BknNetworkFrontmatter{Type: "knowledge_network", ID: "net", Name: "Net"},
		ObjectTypes: []*BknObjectType{{
			BknObjectTypeFrontmatter: BknObjectTypeFrontmatter{Type: "object_type", ID: "customer", Name: "Customer"},
			HasDataPropertiesSection: true,
			HasKeysSection:           true,
			DataProperties: []*DataProperty{{
				Name: "mobile", DisplayName: "Mobile", Type: "string",
				MaskRule: &maskrule.Rule{Kind: maskrule.KindPartial, KeepStart: &keepStart, KeepEnd: &keepEnd, Replacement: "*"},
			}},
			PrimaryKeys: []string{"mobile"},
			DisplayKey:  "mobile",
		}},
	}

	assert.True(t, ValidateNetwork(network).OK())
	network.ObjectTypes[0].DataProperties[0].Type = "vector"
	result := ValidateNetwork(network)
	assert.False(t, result.OK())
	assert.Contains(t, result.Errors[0].Message, "invalid mask_rule")
}
