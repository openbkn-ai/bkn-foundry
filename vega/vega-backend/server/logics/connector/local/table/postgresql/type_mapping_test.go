// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package postgresql

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

func TestPostgresqlMapType(t *testing.T) {
	c := &PostgresqlConnector{}

	cases := []struct {
		in   string
		want string
	}{
		// Known scalar types.
		{"int4", interfaces.DataType_Integer},
		{"INT4", interfaces.DataType_Integer},
		{"text", interfaces.DataType_Text},
		{"jsonb", interfaces.DataType_Json},
		{"enum", interfaces.DataType_String},
		// Array udt_name values with underscore prefixes remain unsupported.
		{"_int4", interfaces.DataType_Other},
		{"_text", interfaces.DataType_Other},
		// Standard information_schema data_type names.
		{"ARRAY", interfaces.DataType_Other},
		// Unknown source types.
		{"unknown_type", interfaces.DataType_Other},
		{"", interfaces.DataType_Other},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			assert.Equal(t, tc.want, c.MapType(tc.in))
		})
	}
}

func TestPostgresqlConnectorNormalizeColumnType(t *testing.T) {
	c := &PostgresqlConnector{}
	for _, tc := range []struct{ native, want string }{
		{"", ""},
		{"  INT  ", "int"},
		{" DECIMAL(10,2) ", "decimal(10,2)"},
	} {
		t.Run(tc.native, func(t *testing.T) {
			assert.Equal(t, tc.want, c.normalizeColumnType(tc.native))
		})
	}
}
