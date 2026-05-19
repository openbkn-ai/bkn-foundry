// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package postgresql

import (
	"testing"

	"vega-backend/interfaces"
)

func TestPostgresqlMapType(t *testing.T) {
	c := &PostgresqlConnector{}

	cases := []struct {
		in   string
		want string
	}{
		// 已知标量类型
		{"int4", interfaces.DataType_Integer},
		{"INT4", interfaces.DataType_Integer},
		{"  text  ", interfaces.DataType_Text},
		{"jsonb", interfaces.DataType_Json},
		// 数组类型：udt_name 形式（带下划线前缀）—— 不识别
		{"_int4", interfaces.DataType_Other},
		{"_text", interfaces.DataType_Other},
		// data_type 形式
		{"ARRAY", interfaces.DataType_Other},
		// 完全未知
		{"unknown_type", interfaces.DataType_Other},
		{"", interfaces.DataType_Other},
	}

	for _, tc := range cases {
		if got := c.MapType(tc.in); got != tc.want {
			t.Errorf("MapType(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
