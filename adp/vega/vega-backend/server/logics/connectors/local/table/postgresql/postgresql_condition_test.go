// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package postgresql

import (
	"context"
	"strings"
	"testing"

	"vega-backend/interfaces"
	"vega-backend/logics/filter_condition"
)

func testFieldsMap() map[string]*interfaces.Property {
	return map[string]*interfaces.Property{
		"age":        {Name: "age", OriginalName: "age", Type: interfaces.DataType_Integer},
		"created_at": {Name: "created_at", OriginalName: "created_at", Type: interfaces.DataType_Datetime},
	}
}

func mustNewCond(t *testing.T, name, op string, value any) interfaces.FilterCondition {
	t.Helper()
	cfg := &interfaces.FilterCondCfg{
		Name:      name,
		Operation: op,
		ValueOptCfg: interfaces.ValueOptCfg{
			ValueFrom: interfaces.ValueFrom_Const,
			Value:     value,
		},
	}
	cond, err := filter_condition.NewFilterCondition(context.Background(), cfg, testFieldsMap())
	if err != nil {
		t.Fatalf("NewFilterCondition(%s, %s) failed: %v", name, op, err)
	}
	return cond
}

func toSQL(t *testing.T, connector *PostgresqlConnector, cond interfaces.FilterCondition) (string, []interface{}) {
	t.Helper()
	sqlizer, err := connector.ConvertFilterCondition(context.Background(), cond, testFieldsMap())
	if err != nil {
		t.Fatalf("ConvertFilterCondition failed: %v", err)
	}
	sql, args, err := sqlizer.ToSql()
	if err != nil {
		t.Fatalf("ToSql failed: %v", err)
	}
	return sql, args
}

func TestConvertGteKeepsNonDateFieldAsParameter(t *testing.T) {
	c := &PostgresqlConnector{}
	cond := mustNewCond(t, "age", ">=", 18)
	sql, args := toSQL(t, c, cond)
	if sql != `"age" >= ?` {
		t.Errorf("unexpected SQL: %s", sql)
	}
	if len(args) != 1 || args[0] != 18 {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestConvertDateGteUsesToTimestamp(t *testing.T) {
	c := &PostgresqlConnector{}
	cond := mustNewCond(t, "created_at", ">=", float64(1710000000000))
	sql, args := toSQL(t, c, cond)
	if sql != `"created_at" >= to_timestamp(?/1000)` {
		t.Errorf("unexpected SQL: %s", sql)
	}
	if len(args) != 1 || args[0] != int64(1710000000000) {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestConvertDateRangeUsesToTimestamp(t *testing.T) {
	c := &PostgresqlConnector{}
	cond := mustNewCond(t, "created_at", "range", []any{1710000000000, 1710003600000})
	sql, args := toSQL(t, c, cond)
	if !strings.Contains(sql, `"created_at" >= to_timestamp(?/1000)`) ||
		!strings.Contains(sql, `"created_at" <= to_timestamp(?/1000)`) {
		t.Errorf("unexpected SQL: %s", sql)
	}
	if len(args) != 2 || args[0] != int64(1710000000000) || args[1] != int64(1710003600000) {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestConvertDateOutRangeUsesToTimestamp(t *testing.T) {
	c := &PostgresqlConnector{}
	cond := mustNewCond(t, "created_at", "out_range", []any{1710000000000, 1710003600000})
	sql, args := toSQL(t, c, cond)
	if !strings.Contains(sql, `"created_at" < to_timestamp(?/1000)`) ||
		!strings.Contains(sql, `"created_at" > to_timestamp(?/1000)`) {
		t.Errorf("unexpected SQL: %s", sql)
	}
	if len(args) != 2 || args[0] != int64(1710000000000) || args[1] != int64(1710003600000) {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestConvertDateBetweenUsesToTimestamp(t *testing.T) {
	c := &PostgresqlConnector{}
	cond := mustNewCond(t, "created_at", "between", []any{1710000000000, 1710003600000})
	sql, args := toSQL(t, c, cond)
	if !strings.Contains(sql, `"created_at" >= to_timestamp(?/1000)`) ||
		!strings.Contains(sql, `"created_at" <= to_timestamp(?/1000)`) {
		t.Errorf("unexpected SQL: %s", sql)
	}
	if len(args) != 2 || args[0] != int64(1710000000000) || args[1] != int64(1710003600000) {
		t.Errorf("unexpected args: %v", args)
	}
}
