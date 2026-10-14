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
