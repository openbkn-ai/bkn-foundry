// Copyright (c) 2026 OpenBKN. All rights reserved.
// Licensed under the Apache License, Version 2.0. See the LICENSE file in the project root.

package bkn

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSerializeObjectType_CellEscapesRoundTrip locks the contract this file exists for: a
// description carrying a line break or a pipe must come back from the file byte for byte, and
// the file itself must still hold one row per property.
func TestSerializeObjectType_CellEscapesRoundTrip(t *testing.T) {
	original := &BknObjectType{
		BknObjectTypeFrontmatter: BknObjectTypeFrontmatter{Type: "object_type", ID: "bom", Name: "BOM"},
		DataSource:               &ResourceInfo{Type: "resource", ID: "res-1"},
		DataProperties: []*DataProperty{
			{Name: "seq_no", DisplayName: "序号", Type: "integer",
				Description: "第一行。\n第二行，含竖线 a|b。", MappedField: "seq_no"},
			{Name: "standard_usage", DisplayName: "标准用量", Type: "decimal",
				Description: "组装时的标准用量。", MappedField: "standard_usage"},
		},
	}

	text := SerializeObjectType(original)
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "| seq_no") {
			assert.Contains(t, line, "<br>", "line break must be escaped inside the row")
			assert.Contains(t, line, `a\|b`, "pipe must be escaped inside the row")
			assert.True(t, strings.HasSuffix(line, "|"), "row must close on its own line")
		}
	}

	parsed, err := ParseObjectTypeFile(text, "/test/bom.bkn")
	require.NoError(t, err)
	require.Len(t, parsed.DataProperties, 2)
	assert.Equal(t, original.DataProperties[0].Description, parsed.DataProperties[0].Description)
	assert.Equal(t, "seq_no", parsed.DataProperties[0].MappedField)
	assert.Equal(t, "standard_usage", parsed.DataProperties[1].Name)
	assert.Equal(t, "standard_usage", parsed.DataProperties[1].MappedField)
}

// TestParseObjectType_LegacyBrokenRowIsJoined covers files exported before cells were escaped:
// the row with the line break arrives split across two lines. Every row after the break used
// to be lost and the broken row lost its Mapped Field; both must now import intact.
func TestParseObjectType_LegacyBrokenRowIsJoined(t *testing.T) {
	text := `---
type: object_type
id: bom
name: BOM
---

## ObjectType: BOM

### Data Source

| Type | ID | Name |
|------|----|------|
| resource | res-1 |  |

### Data Properties

| Name | Display Name | Type | Description | Mapped Field |
|------|--------------|------|-------------|--------------|
| material_code | 物料编码 | string |  | material_code |
| seq_no | 序号 | integer | 序号用来跟踪一个产品中每个物料的位置。比如产品A
A产品的BOM中都有一个序号。 | seq_no |
| standard_usage | 标准用量 | decimal | 组装时的标准用量。 | standard_usage |
| variable_loss_rate | 变动损耗率 | decimal | 0 就是没有损耗。 | variable_loss_rate |

### Logic Properties

`
	parsed, err := ParseObjectTypeFile(text, "/test/bom.bkn")
	require.NoError(t, err)
	require.Len(t, parsed.DataProperties, 4)
	names := make([]string, 0, 4)
	for _, dp := range parsed.DataProperties {
		names = append(names, dp.Name)
		assert.Equal(t, dp.Name, dp.MappedField, "mapped field of %s", dp.Name)
	}
	assert.Equal(t, []string{"material_code", "seq_no", "standard_usage", "variable_loss_rate"}, names)
	assert.Equal(t, "序号用来跟踪一个产品中每个物料的位置。比如产品A\nA产品的BOM中都有一个序号。", parsed.DataProperties[1].Description)
}

// TestParseObjectType_MalformedPropertyRowIsRejected: a row that cannot be rebuilt (the file ends
// inside it) is an error naming the row, not a silently shorter object type.
func TestParseObjectType_MalformedPropertyRowIsRejected(t *testing.T) {
	text := `---
type: object_type
id: bom
name: BOM
---

## ObjectType: BOM

### Data Properties

| Name | Display Name | Type | Description | Mapped Field |
|------|--------------|------|-------------|--------------|
| material_code | 物料编码 | string |  | material_code |
|  | 缺名 | string |  | x |
`
	_, err := ParseObjectTypeFile(text, "/test/bom.bkn")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "row 2")
}

func TestSplitRow_EscapesAndBreaks(t *testing.T) {
	assert.Equal(t, []string{"a|b", "x\ny", "z"}, splitRow(`| a\|b | x<br>y | z |`))
	assert.Equal(t, []string{"plain", "cells"}, splitRow("| plain | cells |"))
}
