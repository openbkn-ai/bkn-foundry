// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mariadb

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

func TestMariaDBConnectorMapType(t *testing.T) {
	connector := &MariaDBConnector{}

	tests := []struct {
		name       string
		nativeType string
		want       string
	}{
		{
			name:       "integer",
			nativeType: "int",
			want:       interfaces.DataType_Integer,
		},
		{
			name:       "unsigned integer",
			nativeType: "bigint unsigned",
			want:       interfaces.DataType_UnsignedInteger,
		},
		{"unsigned integer with display width", "int(11) unsigned", interfaces.DataType_UnsignedInteger},
		{"uppercase unsigned suffix", "BIGINT(20) UNSIGNED", interfaces.DataType_UnsignedInteger},
		{"unsigned tinyint with display width", "tinyint(1) unsigned", interfaces.DataType_UnsignedInteger},
		{"unsigned integer with display width and zerofill", "int(11) unsigned zerofill", interfaces.DataType_UnsignedInteger},
		{"unsigned zerofill integer", "bigint unsigned zerofill", interfaces.DataType_UnsignedInteger},
		{"tinyint flag remains integer", "tinyint(1)", interfaces.DataType_Integer},
		{"boolean alias remains integer", "boolean", interfaces.DataType_Integer},
		{"bool alias remains integer", "BOOL", interfaces.DataType_Integer},
		{"bit without width", "BIT", interfaces.DataType_Binary},
		{"single bit", "bit(1)", interfaces.DataType_Binary},
		{"eight bits", "BIT(8)", interfaces.DataType_Binary},
		{"multiple bytes", "bit(17)", interfaces.DataType_Binary},
		{"maximum bit width", "bit(64)", interfaces.DataType_Binary},
		{"signed integer with display width", "int(11)", interfaces.DataType_Integer},
		{"unsigned float", "float unsigned", interfaces.DataType_Float},
		{"unsigned double", "double unsigned", interfaces.DataType_Float},
		{"unsigned real", "real unsigned", interfaces.DataType_Float},
		{"unsigned double precision", "double precision unsigned", interfaces.DataType_Float},
		{"unsigned float with precision", "float(10,2) unsigned zerofill", interfaces.DataType_Float},
		{"unsigned decimal with precision", "decimal(20,4) unsigned", interfaces.DataType_Decimal},
		{"unsigned decimal without precision", "decimal unsigned", interfaces.DataType_Decimal},
		{"enum labels are not modifiers", "enum('unsigned','zerofill','A)B')", interfaces.DataType_String},
		{"compressed varchar", "varchar(1000) /*M!100301 COMPRESSED*/", interfaces.DataType_String},
		{"compressed text", "longtext /*M!100301 COMPRESSED*/", interfaces.DataType_Text},
		{"comment after numeric modifiers", "int(11) unsigned zerofill /* comment */", interfaces.DataType_UnsignedInteger},
		{"enum labels contain comment markers", "enum('/*literal*/','zerofill','A)B')", interfaces.DataType_String},
		{
			name:       "strips length suffix",
			nativeType: "varchar(255)",
			want:       interfaces.DataType_String,
		},
		{
			name:       "enum with labels",
			nativeType: "enum('pending','completed')",
			want:       interfaces.DataType_String,
		},
		{
			name:       "set with labels",
			nativeType: "set('red','blue')",
			want:       interfaces.DataType_String,
		},
		{
			name:       "normalizes case",
			nativeType: "DATETIME",
			want:       interfaces.DataType_Datetime,
		},
		{
			name:       "json",
			nativeType: "json",
			want:       interfaces.DataType_Json,
		},
		{
			name:       "unknown type",
			nativeType: "geometry",
			want:       interfaces.DataType_Other,
		},
		{
			name:       "empty type",
			nativeType: "",
			want:       interfaces.DataType_Other,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, connector.MapType(tt.nativeType))
		})
	}
}

func TestMariaDBConnectorNormalizeColumnType(t *testing.T) {
	c := &MariaDBConnector{}
	for _, tc := range []struct{ native, want string }{
		{"", ""},
		{"  INT  ", "int"},
		{" DECIMAL(10,2) ", "decimal(10,2)"},
		{" INT(11) UNSIGNED ", "int(11) UNSIGNED"},
		{` ENUM('Active','Inactive') `, `enum('Active','Inactive')`},
		{` SET('Read','Write') `, `set('Read','Write')`},
		{` ENUM('It''S','A\'B','C\\D','尾巴') `, `enum('It''S','A\'B','C\\D','尾巴')`},
		{` ENUM("Active","A""B") `, `enum("Active","A""B")`},
	} {
		t.Run(tc.native, func(t *testing.T) {
			assert.Equal(t, tc.want, c.normalizeColumnType(tc.native))
		})
	}
}
