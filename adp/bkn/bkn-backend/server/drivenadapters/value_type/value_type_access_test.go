// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package value_type

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

func newAccess(t *testing.T) (*valueTypeAccess, sqlmock.Sqlmock) {
	t.Helper()
	db, smock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return &valueTypeAccess{db: db}, smock
}

func valueTypeRow(definition any) *sqlmock.Rows {
	return sqlmock.NewRows(valueTypeColumns).AddRow(
		"lis_status", "检验申请状态", "", `"lis"`, "", "", "raw", "kn1", "main", "string", definition,
		"u1", "user", int64(1), "u1", "user", int64(2),
	)
}

func TestCreateValueTypes(t *testing.T) {
	vta, smock := newAccess(t)
	min := 1
	vt := &interfaces.ValueType{
		VTID: "rating", VTName: "满意度", KNID: "kn1", Branch: "main", BaseType: "integer",
		ValueTypeDefinition: interfaces.ValueTypeDefinition{Length: &interfaces.LengthRange{Min: &min}},
	}
	smock.ExpectBegin()
	smock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_value_type (f_id,f_name,f_comment,f_tags,f_icon,f_color,f_bkn_raw_content,f_kn_id,f_branch,f_base_type,f_definition,")).
		WithArgs("rating", "满意度", "", "", "", "", "", "kn1", "main", "integer", `{"length":{"min":1}}`,
			"", "", int64(0), "", "", int64(0)).
		WillReturnResult(sqlmock.NewResult(1, 1))
	smock.ExpectCommit()

	tx, err := vta.db.Begin()
	require.NoError(t, err)
	require.NoError(t, vta.CreateValueTypes(context.Background(), tx, []*interfaces.ValueType{vt}))
	require.NoError(t, tx.Commit())
	assert.NoError(t, smock.ExpectationsWereMet())
}

func TestReadValueTypes(t *testing.T) {
	t.Run("decodes the definition", func(t *testing.T) {
		vta, smock := newAccess(t)
		smock.ExpectQuery("SELECT .* FROM t_value_type WHERE f_kn_id = \\? AND f_branch = \\? ORDER BY f_id").
			WithArgs("kn1", "main").
			WillReturnRows(valueTypeRow(`{"enum":[{"value":"A","display":"已申请"},{"value":"C","display":"已撤销","deprecated":true}]}`))
		list, err := vta.ListValueTypes(context.Background(), interfaces.ValueTypesQueryParams{KNID: "kn1"})
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, []string{"lis"}, list[0].Tags)
		assert.Equal(t, interfaces.MODULE_TYPE_VALUE_TYPE, list[0].ModuleType)
		require.Len(t, list[0].Enum, 2)
		assert.True(t, list[0].Enum[1].Deprecated)
	})

	t.Run("an empty definition declares no rules", func(t *testing.T) {
		vta, smock := newAccess(t)
		smock.ExpectQuery("SELECT .* FROM t_value_type").WillReturnRows(valueTypeRow(nil))
		list, err := vta.ListValueTypes(context.Background(), interfaces.ValueTypesQueryParams{KNID: "kn1"})
		require.NoError(t, err)
		assert.Equal(t, interfaces.ValueTypeDefinition{}, list[0].ValueTypeDefinition)
	})

	t.Run("reads inside the transaction it is given", func(t *testing.T) {
		vta, smock := newAccess(t)
		smock.ExpectBegin()
		smock.ExpectQuery("SELECT .* FROM t_value_type WHERE f_kn_id = \\? AND f_branch = \\? AND f_id IN \\(\\?\\)").
			WithArgs("kn1", "main", "lis_status").
			WillReturnRows(valueTypeRow(`{}`))
		smock.ExpectRollback()
		tx, err := vta.db.Begin()
		require.NoError(t, err)
		list, err := vta.GetValueTypesByIDs(context.Background(), tx, "kn1", "main", []string{"lis_status"})
		require.NoError(t, err)
		assert.Len(t, list, 1)
		require.NoError(t, tx.Rollback())
		assert.NoError(t, smock.ExpectationsWereMet())
	})
}
