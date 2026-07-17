// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mariadb

import (
	"context"
	"strings"
	"testing"

	"vega-backend/interfaces"
	"vega-backend/logics/filter_condition"
)

func TestQuoteColumnName(t *testing.T) {
	t.Run("quote column name simple", func(t *testing.T) {
		if got := quoteColumnName("name"); got != "`name`" {
			t.Errorf("expected `name`, got %s", got)
		}
	})
	t.Run("quote column name with alias", func(t *testing.T) {
		got := quoteColumnName("t1.name")
		if got != "`t1`.`name`" {
			t.Errorf("expected `t1`.`name`, got %s", got)
		}
	})
	t.Run("quote column name empty", func(t *testing.T) {
		if got := quoteColumnName(""); got != "``" {
			t.Errorf("expected ``, got %s", got)
		}
	})
	t.Run("quote column name with backtick", func(t *testing.T) {
		got := quoteColumnName("col`name")
		if got != "`col``name`" {
			t.Errorf("expected `col``name`, got %s", got)
		}
	})
	t.Run("quote column name alias with spaces", func(t *testing.T) {
		got := quoteColumnName(" t1 . name ")
		if got != "`t1`.`name`" {
			t.Errorf("expected `t1`.`name`, got %s", got)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionEqual(t *testing.T) {
	t.Run("convert equal const", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "name", "==", "alice")
		sql, args := toSQL(t, c, cond)
		if sql != "`name` = ?" {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 1 || args[0] != "alice" {
			t.Errorf("unexpected args: %v", args)
		}
	})
	t.Run("convert equal field to field", func(t *testing.T) {
		c := &MariaDBConnector{}
		cfg := &interfaces.FilterCondCfg{
			Name:      "name",
			Operation: "==",
			ValueOptCfg: interfaces.ValueOptCfg{
				ValueFrom: interfaces.ValueFrom_Field,
				Value:     "tags",
			},
		}
		cond, err := filter_condition.NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		sql, _ := toSQL(t, c, cond)
		if sql != "`name` = `tags`" {
			t.Errorf("unexpected SQL: %s", sql)
		}
	})
	t.Run("convert equal alias column", func(t *testing.T) {
		c := &MariaDBConnector{}
		cfg := &interfaces.FilterCondCfg{
			Name:      "alias_col",
			Operation: "==",
			ValueOptCfg: interfaces.ValueOptCfg{
				ValueFrom: interfaces.ValueFrom_Const,
				Value:     "test",
			},
		}
		cond, err := filter_condition.NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		sql, _ := toSQL(t, c, cond)
		// OriginalName 是 "t1.col"，应该生成 `t1`.`col`
		if sql != "`t1`.`col` = ?" {
			t.Errorf("unexpected SQL: %s", sql)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionNotEqual(t *testing.T) {
	t.Run("convert not equal", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "name", "!=", "bob")
		sql, args := toSQL(t, c, cond)
		if sql != "`name` <> ?" {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 1 || args[0] != "bob" {
			t.Errorf("unexpected args: %v", args)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionGt(t *testing.T) {
	t.Run("convert gt", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "age", ">", 18)
		sql, args := toSQL(t, c, cond)
		if sql != "`age` > ?" {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 1 || args[0] != 18 {
			t.Errorf("unexpected args: %v", args)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionGte(t *testing.T) {
	t.Run("convert gte", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "age", ">=", 18)
		sql, _ := toSQL(t, c, cond)
		if sql != "`age` >= ?" {
			t.Errorf("unexpected SQL: %s", sql)
		}
	})
	t.Run("convert date gte uses from unix time", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "created_at", ">=", float64(1710000000000))
		sql, args := toSQL(t, c, cond)
		if sql != "`created_at` >= FROM_UNIXTIME(?/1000)" {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 1 || args[0] != int64(1710000000000) {
			t.Errorf("unexpected args: %v", args)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionLt(t *testing.T) {
	t.Run("convert lt", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "age", "<", 65)
		sql, _ := toSQL(t, c, cond)
		if sql != "`age` < ?" {
			t.Errorf("unexpected SQL: %s", sql)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionLte(t *testing.T) {
	t.Run("convert lte", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "age", "<=", 65)
		sql, _ := toSQL(t, c, cond)
		if sql != "`age` <= ?" {
			t.Errorf("unexpected SQL: %s", sql)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionIn(t *testing.T) {
	t.Run("convert in", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "name", "in", []any{"alice", "bob"})
		sql, args := toSQL(t, c, cond)
		if sql != "`name` IN (?,?)" {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 2 {
			t.Errorf("expected 2 args, got %d", len(args))
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionNotIn(t *testing.T) {
	t.Run("convert not in", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "name", "not_in", []any{"alice"})
		sql, _ := toSQL(t, c, cond)
		if sql != "`name` NOT IN (?)" {
			t.Errorf("unexpected SQL: %s", sql)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionLike(t *testing.T) {
	t.Run("convert like", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "name", "like", "ali")
		sql, args := toSQL(t, c, cond)
		if sql != "`name` LIKE ?" {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 1 || args[0] != "%ali%" {
			t.Errorf("unexpected args: %v", args)
		}
	})
	t.Run("convert like special chars", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "name", "like", "100%")
		_, args := toSQL(t, c, cond)
		argStr, ok := args[0].(string)
		if !ok {
			t.Fatalf("expected string arg, got %T", args[0])
		}
		// % 应被转义为 \%
		if !strings.Contains(argStr, `\%`) {
			t.Errorf("expected escaped %%, got %s", argStr)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionNull(t *testing.T) {
	t.Run("convert null", func(t *testing.T) {
		c := &MariaDBConnector{}
		cfg := &interfaces.FilterCondCfg{Name: "name", Operation: "null"}
		cond, _ := filter_condition.NewFilterCondition(context.Background(), cfg, testFieldsMap())
		sql, _ := toSQL(t, c, cond)
		if sql != "`name` IS NULL" {
			t.Errorf("unexpected SQL: %s", sql)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionNotNull(t *testing.T) {
	t.Run("convert not null", func(t *testing.T) {
		c := &MariaDBConnector{}
		cfg := &interfaces.FilterCondCfg{Name: "name", Operation: "not_null"}
		cond, _ := filter_condition.NewFilterCondition(context.Background(), cfg, testFieldsMap())
		sql, _ := toSQL(t, c, cond)
		if sql != "`name` IS NOT NULL" {
			t.Errorf("unexpected SQL: %s", sql)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionEmpty(t *testing.T) {
	t.Run("convert empty", func(t *testing.T) {
		c := &MariaDBConnector{}
		cfg := &interfaces.FilterCondCfg{Name: "name", Operation: "empty"}
		cond, _ := filter_condition.NewFilterCondition(context.Background(), cfg, testFieldsMap())
		sql, args := toSQL(t, c, cond)
		if sql != "`name` = ?" {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 1 || args[0] != "" {
			t.Errorf("unexpected args: %v", args)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionRange(t *testing.T) {
	t.Run("convert range", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "age", "range", []any{18, 65})
		sql, args := toSQL(t, c, cond)
		if !strings.Contains(sql, "`age` >= ?") || !strings.Contains(sql, "`age` <= ?") {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 2 {
			t.Errorf("expected 2 args, got %d", len(args))
		}
	})
	t.Run("convert date range uses from unix time", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "created_at", "range", []any{1710000000000, 1710003600000})
		sql, args := toSQL(t, c, cond)
		if !strings.Contains(sql, "`created_at` >= FROM_UNIXTIME(?/1000)") ||
			!strings.Contains(sql, "`created_at` <= FROM_UNIXTIME(?/1000)") {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 2 || args[0] != int64(1710000000000) || args[1] != int64(1710003600000) {
			t.Errorf("unexpected args: %v", args)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionOutRange(t *testing.T) {
	t.Run("convert date out range uses from unix time", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "created_at", "out_range", []any{1710000000000, 1710003600000})
		sql, args := toSQL(t, c, cond)
		if !strings.Contains(sql, "`created_at` < FROM_UNIXTIME(?/1000)") ||
			!strings.Contains(sql, "`created_at` > FROM_UNIXTIME(?/1000)") {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 2 || args[0] != int64(1710000000000) || args[1] != int64(1710003600000) {
			t.Errorf("unexpected args: %v", args)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionBetween(t *testing.T) {
	t.Run("convert date between uses from unix time", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "created_at", "between", []any{1710000000000, 1710003600000})
		sql, args := toSQL(t, c, cond)
		if !strings.Contains(sql, "`created_at` >= FROM_UNIXTIME(?/1000)") ||
			!strings.Contains(sql, "`created_at` <= FROM_UNIXTIME(?/1000)") {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 2 || args[0] != int64(1710000000000) || args[1] != int64(1710003600000) {
			t.Errorf("unexpected args: %v", args)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionRegex(t *testing.T) {
	t.Run("convert regex", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "name", "regex", "^ali.*")
		sql, args := toSQL(t, c, cond)
		if sql != "`name` REGEXP ?" {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 1 || args[0] != "^ali.*" {
			t.Errorf("unexpected args: %v", args)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionTrue(t *testing.T) {
	t.Run("convert true", func(t *testing.T) {
		c := &MariaDBConnector{}
		cfg := &interfaces.FilterCondCfg{Name: "is_active", Operation: "true"}
		cond, _ := filter_condition.NewFilterCondition(context.Background(), cfg, testFieldsMap())
		sql, args := toSQL(t, c, cond)
		if sql != "`is_active` = ?" {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 1 || args[0] != true {
			t.Errorf("unexpected args: %v", args)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionPrefix(t *testing.T) {
	t.Run("convert prefix", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "name", "prefix", "ali")
		sql, args := toSQL(t, c, cond)
		if sql != "`name` LIKE ?" {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 1 || args[0] != "ali%" {
			t.Errorf("unexpected args: %v", args)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionAnd(t *testing.T) {
	t.Run("convert and", func(t *testing.T) {
		c := &MariaDBConnector{}
		cfg := &interfaces.FilterCondCfg{
			Operation: "and",
			SubConds: []*interfaces.FilterCondCfg{
				{Name: "name", Operation: "==", ValueOptCfg: interfaces.ValueOptCfg{ValueFrom: "const", Value: "alice"}},
				{Name: "age", Operation: ">", ValueOptCfg: interfaces.ValueOptCfg{ValueFrom: "const", Value: 18}},
			},
		}
		cond, err := filter_condition.NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		sql, args := toSQL(t, c, cond)
		if !strings.Contains(sql, "`name` = ?") || !strings.Contains(sql, "`age` > ?") {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if !strings.Contains(sql, " AND ") {
			t.Errorf("expected AND in SQL: %s", sql)
		}
		if len(args) != 2 {
			t.Errorf("expected 2 args, got %d", len(args))
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionOr(t *testing.T) {
	t.Run("convert or", func(t *testing.T) {
		c := &MariaDBConnector{}
		cfg := &interfaces.FilterCondCfg{
			Operation: "or",
			SubConds: []*interfaces.FilterCondCfg{
				{Name: "name", Operation: "==", ValueOptCfg: interfaces.ValueOptCfg{ValueFrom: "const", Value: "alice"}},
				{Name: "name", Operation: "==", ValueOptCfg: interfaces.ValueOptCfg{ValueFrom: "const", Value: "bob"}},
			},
		}
		cond, err := filter_condition.NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		sql, _ := toSQL(t, c, cond)
		if !strings.Contains(sql, " OR ") {
			t.Errorf("expected OR in SQL: %s", sql)
		}
	})
}

func TestSpecialReplacer(t *testing.T) {
	t.Run("special replacer", func(t *testing.T) {
		tests := []struct {
			input    string
			expected string
		}{
			{`hello`, `hello`},
			{`%`, `\%`},
			{`_`, `\_`},
			{`'`, `\'`},
			{`\`, `\\\\`},
			{`100%_done`, `100\%\_done`},
		}
		for _, tt := range tests {
			got := Special.Replace(tt.input)
			if got != tt.expected {
				t.Errorf("Special.Replace(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		}
	})
}

func testFieldsMap() map[string]*interfaces.Property {
	return map[string]*interfaces.Property{
		"name":       {Name: "name", OriginalName: "name", Type: interfaces.DataType_String},
		"age":        {Name: "age", OriginalName: "age", Type: interfaces.DataType_Integer},
		"score":      {Name: "score", OriginalName: "score", Type: interfaces.DataType_Float},
		"created_at": {Name: "created_at", OriginalName: "created_at", Type: interfaces.DataType_Datetime},
		"is_active":  {Name: "is_active", OriginalName: "is_active", Type: interfaces.DataType_Boolean},
		"tags":       {Name: "tags", OriginalName: "tags", Type: interfaces.DataType_Text},
		"alias_col":  {Name: "alias_col", OriginalName: "t1.col", Type: interfaces.DataType_String},
	}
}

func toSQL(t *testing.T, connector *MariaDBConnector, cond interfaces.FilterCondition) (string, []interface{}) {
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
