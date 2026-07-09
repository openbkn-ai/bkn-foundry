// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mariadb

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"vega-backend/interfaces"
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
			name:       "normalizes case and whitespace",
			nativeType: "  DATETIME  ",
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
