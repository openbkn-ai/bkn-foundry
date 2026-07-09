// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package sql

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
	"vega-backend/logics/filter_condition"
)

func TestLogicViewSQLConvertFilterConditionAdditional(t *testing.T) {
	fields := testSQLConditionFieldMap()
	generator := NewlogicDefinitionSQLGenerator(testSQLView())

	tests := []struct {
		name    string
		cfg     *interfaces.FilterCondCfg
		wantSQL string
		wantArg []any
	}{
		{
			name:    "not equal const",
			cfg:     sqlConditionCfg("age", filter_condition.OperationNotEqual, interfaces.ValueFrom_Const, 18),
			wantSQL: "`age` <> ?",
			wantArg: []any{18},
		},
		{
			name:    "gte field",
			cfg:     sqlConditionCfg("age", filter_condition.OperationGte, interfaces.ValueFrom_Field, "score"),
			wantSQL: "`age` >= `score`",
		},
		{
			name:    "in const slice",
			cfg:     sqlConditionCfg("name", filter_condition.OperationIn, interfaces.ValueFrom_Const, []any{"alice", "bob"}),
			wantSQL: "`name` IN (?,?)",
			wantArg: []any{"alice", "bob"},
		},
		{
			name:    "not in const slice",
			cfg:     sqlConditionCfg("name", filter_condition.OperationNotIn, interfaces.ValueFrom_Const, []any{"alice", "bob"}),
			wantSQL: "`name` NOT IN (?,?)",
			wantArg: []any{"alice", "bob"},
		},
		{
			name:    "not like escapes special chars",
			cfg:     sqlConditionCfg("name", filter_condition.OperationNotLike, interfaces.ValueFrom_Const, "a_%"),
			wantSQL: "`name` NOT LIKE ?",
			wantArg: []any{`%a\_\%%`},
		},
		{
			name:    "contain values",
			cfg:     sqlConditionCfg("tags", filter_condition.OperationContain, interfaces.ValueFrom_Const, []any{"core", "pii"}),
			wantSQL: "(FIND_IN_SET(?, `tags`) > 0 AND FIND_IN_SET(?, `tags`) > 0)",
			wantArg: []any{"core", "pii"},
		},
		{
			name:    "not contain values",
			cfg:     sqlConditionCfg("tags", filter_condition.OperationNotContain, interfaces.ValueFrom_Const, []any{"core", "pii"}),
			wantSQL: "(FIND_IN_SET(?, `tags`) = 0 OR FIND_IN_SET(?, `tags`) = 0)",
			wantArg: []any{"core", "pii"},
		},
		{
			name:    "range values",
			cfg:     sqlConditionCfg("age", filter_condition.OperationRange, interfaces.ValueFrom_Const, []any{18, 30}),
			wantSQL: "(`age` >= ? AND `age` <= ?)",
			wantArg: []any{18, 30},
		},
		{
			name:    "out range values",
			cfg:     sqlConditionCfg("age", filter_condition.OperationOutRange, interfaces.ValueFrom_Const, []any{18, 30}),
			wantSQL: "(`age` < ? OR `age` > ?)",
			wantArg: []any{18, 30},
		},
		{
			name:    "null",
			cfg:     sqlConditionCfg("name", filter_condition.OperationNull, interfaces.ValueFrom_Const, nil),
			wantSQL: "`name` IS NULL",
		},
		{
			name:    "not null",
			cfg:     sqlConditionCfg("name", filter_condition.OperationNotNull, interfaces.ValueFrom_Const, nil),
			wantSQL: "`name` IS NOT NULL",
		},
		{
			name:    "empty",
			cfg:     sqlConditionCfg("name", filter_condition.OperationEmpty, interfaces.ValueFrom_Const, nil),
			wantSQL: "`name` = ?",
			wantArg: []any{""},
		},
		{
			name:    "not empty",
			cfg:     sqlConditionCfg("name", filter_condition.OperationNotEmpty, interfaces.ValueFrom_Const, nil),
			wantSQL: "`name` <> ?",
			wantArg: []any{""},
		},
		{
			name:    "prefix",
			cfg:     sqlConditionCfg("name", filter_condition.OperationPrefix, interfaces.ValueFrom_Const, "Al_"),
			wantSQL: "`name` LIKE ?",
			wantArg: []any{`Al\_%`},
		},
		{
			name:    "not prefix",
			cfg:     sqlConditionCfg("name", filter_condition.OperationNotPrefix, interfaces.ValueFrom_Const, "Al_"),
			wantSQL: "`name` NOT LIKE ?",
			wantArg: []any{`Al\_%`},
		},
		{
			name:    "regex",
			cfg:     sqlConditionCfg("name", filter_condition.OperationRegex, interfaces.ValueFrom_Const, "^A"),
			wantSQL: "`name` REGEXP ?",
			wantArg: []any{"^A"},
		},
		{
			name:    "true",
			cfg:     sqlConditionCfg("is_active", filter_condition.OperationTrue, interfaces.ValueFrom_Const, nil),
			wantSQL: "`is_active` = ?",
			wantArg: []any{true},
		},
		{
			name:    "false",
			cfg:     sqlConditionCfg("is_active", filter_condition.OperationFalse, interfaces.ValueFrom_Const, nil),
			wantSQL: "`is_active` = ?",
			wantArg: []any{false},
		},
		{
			name:    "before",
			cfg:     sqlConditionCfg("created_at", filter_condition.OperationBefore, interfaces.ValueFrom_Const, []any{float64(3), filter_condition.CurrentDay}),
			wantSQL: "`created_at` < DATE_SUB(NOW(), INTERVAL ? day)",
			wantArg: []any{3},
		},
		{
			name:    "current month",
			cfg:     sqlConditionCfg("created_at", filter_condition.OperationCurrent, interfaces.ValueFrom_Const, filter_condition.CurrentMonth),
			wantSQL: "DATE_FORMAT(`created_at`, '%Y-%m') = DATE_FORMAT(NOW(), '%Y-%m')",
		},
		{
			name:    "between values",
			cfg:     sqlConditionCfg("age", filter_condition.OperationBetween, interfaces.ValueFrom_Const, []any{18, 30}),
			wantSQL: "(`age` >= ? AND `age` <= ?)",
			wantArg: []any{18, 30},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cond := mustSQLCondition(t, tt.cfg, fields)

			sqlizer, err := generator.ConvertFilterCondition(context.Background(), cond, fields)
			require.NoError(t, err)
			gotSQL, gotArgs, err := sqlizer.ToSql()

			require.NoError(t, err)
			assert.Equal(t, tt.wantSQL, gotSQL)
			assert.Equal(t, tt.wantArg, gotArgs)
		})
	}
}

func TestLogicViewSQLConvertFilterConditionGroupsAndErrors(t *testing.T) {
	fields := testSQLConditionFieldMap()
	generator := NewlogicDefinitionSQLGenerator(testSQLView())

	t.Run("and/or group converts sub conditions", func(t *testing.T) {
		cond := mustSQLCondition(t, &interfaces.FilterCondCfg{
			Operation: filter_condition.OperationOr,
			SubConds: []*interfaces.FilterCondCfg{
				sqlConditionCfg("age", filter_condition.OperationGt, interfaces.ValueFrom_Const, 18),
				{
					Operation: filter_condition.OperationAnd,
					SubConds: []*interfaces.FilterCondCfg{
						sqlConditionCfg("name", filter_condition.OperationLike, interfaces.ValueFrom_Const, "Al"),
						sqlConditionCfg("is_active", filter_condition.OperationTrue, interfaces.ValueFrom_Const, nil),
					},
				},
			},
		}, fields)

		sqlizer, err := generator.ConvertFilterCondition(context.Background(), cond, fields)
		require.NoError(t, err)
		gotSQL, gotArgs, err := sqlizer.ToSql()

		require.NoError(t, err)
		assert.Equal(t, "(`age` > ? OR (`name` LIKE ? AND `is_active` = ?))", gotSQL)
		assert.Equal(t, []any{18, "%Al%", true}, gotArgs)
	})

	t.Run("unsupported operation returns error", func(t *testing.T) {
		_, err := generator.ConvertFilterConditionWithOpr(context.Background(), unsupportedSQLCondition{}, fields)

		require.Error(t, err)
		assert.ErrorContains(t, err, "not supported")
	})

	t.Run("wrong concrete type returns error", func(t *testing.T) {
		_, err := generator.ConvertFilterConditionEqual(context.Background(), unsupportedSQLCondition{operation: filter_condition.OperationEqual}, fields)

		require.Error(t, err)
		assert.ErrorContains(t, err, "condition is not")
	})

	t.Run("invalid current format returns error", func(t *testing.T) {
		cond := &filter_condition.CurrentCond{
			Lfield: fields["created_at"],
			Cfg:    sqlConditionCfg("created_at", filter_condition.OperationCurrent, interfaces.ValueFrom_Const, "quarter"),
			Value:  "quarter",
		}

		_, err := generator.ConvertFilterConditionCurrent(context.Background(), cond, fields)

		require.Error(t, err)
		assert.ErrorContains(t, err, "unsupported format")
	})

	t.Run("invalid before interval returns error", func(t *testing.T) {
		cond := &filter_condition.BeforeCond{
			Lfield: fields["created_at"],
			Cfg:    sqlConditionCfg("created_at", filter_condition.OperationBefore, interfaces.ValueFrom_Const, []any{"3", filter_condition.CurrentDay}),
			Value:  []any{"3", filter_condition.CurrentDay},
		}

		_, err := generator.ConvertFilterConditionBefore(context.Background(), cond, fields)

		require.Error(t, err)
		assert.ErrorContains(t, err, "interval value should be a number")
	})
}

func sqlConditionCfg(name string, operation string, valueFrom string, value any) *interfaces.FilterCondCfg {
	return &interfaces.FilterCondCfg{
		Name:      name,
		Operation: operation,
		ValueOptCfg: interfaces.ValueOptCfg{
			ValueFrom: valueFrom,
			Value:     value,
		},
	}
}

func testSQLConditionFieldMap() map[string]*interfaces.Property {
	fields := testSQLFieldMap()
	fields["score"] = &interfaces.Property{Name: "score", OriginalName: "score", Type: interfaces.DataType_Integer}
	fields["tags"] = &interfaces.Property{Name: "tags", OriginalName: "tags", Type: interfaces.DataType_String}
	fields["created_at"] = &interfaces.Property{Name: "created_at", OriginalName: "created_at", Type: interfaces.DataType_Datetime}
	fields["is_active"] = &interfaces.Property{Name: "is_active", OriginalName: "is_active", Type: interfaces.DataType_Boolean}
	return fields
}

type unsupportedSQLCondition struct {
	operation string
}

func (u unsupportedSQLCondition) GetOperation() string {
	if u.operation != "" {
		return u.operation
	}
	return "unsupported"
}

func (unsupportedSQLCondition) SupportSubCond() bool { return false }
func (unsupportedSQLCondition) NeedName() bool       { return false }
func (unsupportedSQLCondition) NeedValue() bool      { return false }
func (unsupportedSQLCondition) NeedConstValue() bool { return false }
func (unsupportedSQLCondition) IsSingleValue() bool  { return false }
func (unsupportedSQLCondition) IsFixedLenArrayValue() bool {
	return false
}
func (unsupportedSQLCondition) RequiredValueLen() int { return -1 }
func (unsupportedSQLCondition) New(context.Context, *interfaces.FilterCondCfg, map[string]*interfaces.Property) (interfaces.FilterCondition, error) {
	return nil, nil
}
