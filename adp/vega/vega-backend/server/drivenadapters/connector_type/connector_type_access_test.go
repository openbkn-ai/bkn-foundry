// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package connector_type

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

func TestConnectorTypeAccessGetByType(t *testing.T) {
	access, mock, cleanup := newConnectorTypeAccessMock(t)
	defer cleanup()

	mock.ExpectQuery("SELECT f_type, f_name, f_tags, f_description, f_mode, f_category, f_endpoint, f_field_config, f_enabled FROM t_connector_type WHERE f_type = ?").
		WithArgs("remote-api").
		WillReturnRows(connectorTypeRows().AddRow(
			"remote-api",
			"Remote API",
			"tag-a,tag-b",
			"desc",
			interfaces.ConnectorModeRemote,
			interfaces.ConnectorCategoryAPI,
			"http://remote",
			`{"token":{"name":"Token","type":"string","required":true,"encrypted":true}}`,
			true,
		))

	got, err := access.GetByType(context.Background(), "remote-api")

	require.NoError(t, err)
	assert.Equal(t, "remote-api", got.Type)
	assert.Equal(t, []string{"tag-a", "tag-b"}, got.Tags)
	assert.True(t, got.Enabled)
	require.Contains(t, got.FieldConfig, "token")
	assert.True(t, got.FieldConfig["token"].Encrypted)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestConnectorTypeAccessGetByTypeNotFound(t *testing.T) {
	access, mock, cleanup := newConnectorTypeAccessMock(t)
	defer cleanup()

	mock.ExpectQuery("SELECT f_type, f_name, f_tags, f_description, f_mode, f_category, f_endpoint, f_field_config, f_enabled FROM t_connector_type WHERE f_type = ?").
		WithArgs("missing").
		WillReturnError(sql.ErrNoRows)

	got, err := access.GetByType(context.Background(), "missing")

	require.NoError(t, err)
	assert.Nil(t, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestConnectorTypeAccessGetByName(t *testing.T) {
	access, mock, cleanup := newConnectorTypeAccessMock(t)
	defer cleanup()

	mock.ExpectQuery("SELECT f_type, f_name, f_tags, f_description, f_mode, f_category, f_endpoint, f_field_config, f_enabled FROM t_connector_type WHERE f_name = ?").
		WithArgs("Remote API").
		WillReturnRows(connectorTypeRows().AddRow(
			"remote-api",
			"Remote API",
			"tag-a,tag-b",
			"desc",
			interfaces.ConnectorModeRemote,
			interfaces.ConnectorCategoryAPI,
			"http://remote",
			`{"token":{"name":"Token","type":"string","required":true,"encrypted":true}}`,
			true,
		))

	got, err := access.GetByName(context.Background(), "Remote API")

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "remote-api", got.Type)
	assert.Equal(t, "Remote API", got.Name)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestConnectorTypeAccessList(t *testing.T) {
	access, mock, cleanup := newConnectorTypeAccessMock(t)
	defer cleanup()

	enabled := true
	params := interfaces.ConnectorTypesQueryParams{
		Name:     "remote",
		Mode:     interfaces.ConnectorModeRemote,
		Category: interfaces.ConnectorCategoryAPI,
		Enabled:  &enabled,
	}

	mock.ExpectQuery("SELECT COUNT(*) FROM t_connector_type WHERE f_name LIKE ? AND f_mode = ? AND f_category = ? AND f_enabled = ?").
		WithArgs("%remote%", interfaces.ConnectorModeRemote, interfaces.ConnectorCategoryAPI, true).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("SELECT f_type, f_name, f_tags, f_description, f_mode, f_category, f_endpoint, f_field_config, f_enabled FROM t_connector_type WHERE f_name LIKE ? AND f_mode = ? AND f_category = ? AND f_enabled = ? ORDER BY f_name ASC").
		WithArgs("%remote%", interfaces.ConnectorModeRemote, interfaces.ConnectorCategoryAPI, true).
		WillReturnRows(connectorTypeRows().AddRow("remote-api", "Remote API", "", "", interfaces.ConnectorModeRemote, interfaces.ConnectorCategoryAPI, "", "", true))

	got, total, err := access.List(context.Background(), params)

	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, got, 1)
	assert.Equal(t, "remote-api", got[0].Type)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestConnectorTypeAccessListAuthResources(t *testing.T) {
	access, mock, cleanup := newConnectorTypeAccessMock(t)
	defer cleanup()

	mock.ExpectQuery("SELECT f_type, f_name FROM t_connector_type WHERE f_type = ? AND f_name LIKE ? ORDER BY f_name ASC").
		WithArgs("remote-api", "%Remote%").
		WillReturnRows(sqlmock.NewRows([]string{"f_type", "f_name"}).AddRow("remote-api", "Remote API"))

	got, err := access.ListAuthResources(context.Background(), interfaces.AuthResourceQueryParams{
		ID:      "remote-api",
		Keyword: "Remote",
	})

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "remote-api", got[0].ID)
	assert.Equal(t, interfaces.AuthResourceTypeConnectorType, got[0].Type)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestConnectorTypeAccessExecs(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		access, mock, cleanup := newConnectorTypeAccessMock(t)
		defer cleanup()
		ct := sampleConnectorType()

		mock.ExpectExec("INSERT INTO t_connector_type (f_type,f_name,f_tags,f_description,f_mode,f_category,f_endpoint,f_field_config,f_enabled) VALUES (?,?,?,?,?,?,?,?,?)").
			WithArgs(ct.Type, ct.Name, `"tag-a","tag-b"`, ct.Description, ct.Mode, ct.Category, ct.Endpoint, `{"token":{"name":"Token","type":"string","description":"","required":true,"encrypted":true}}`, ct.Enabled).
			WillReturnResult(sqlmock.NewResult(1, 1))

		require.NoError(t, access.Create(context.Background(), ct))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("update", func(t *testing.T) {
		access, mock, cleanup := newConnectorTypeAccessMock(t)
		defer cleanup()
		ct := sampleConnectorType()
		ct.Name = "Remote API Updated"

		mock.ExpectExec("UPDATE t_connector_type SET f_name = ?, f_tags = ?, f_description = ?, f_mode = ?, f_category = ?, f_endpoint = ?, f_field_config = ?, f_enabled = ? WHERE f_type = ?").
			WithArgs(ct.Name, `"tag-a","tag-b"`, ct.Description, ct.Mode, ct.Category, ct.Endpoint, `{"token":{"name":"Token","type":"string","description":"","required":true,"encrypted":true}}`, ct.Enabled, ct.Type).
			WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, access.Update(context.Background(), ct))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("set enabled", func(t *testing.T) {
		access, mock, cleanup := newConnectorTypeAccessMock(t)
		defer cleanup()

		mock.ExpectExec("UPDATE t_connector_type SET f_enabled = ? WHERE f_type = ?").
			WithArgs(false, "remote-api").
			WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, access.SetEnabled(context.Background(), "remote-api", false))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("delete", func(t *testing.T) {
		access, mock, cleanup := newConnectorTypeAccessMock(t)
		defer cleanup()

		mock.ExpectExec("DELETE FROM t_connector_type WHERE f_type = ?").
			WithArgs("remote-api").
			WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, access.DeleteByType(context.Background(), "remote-api"))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("delete returns db error", func(t *testing.T) {
		access, mock, cleanup := newConnectorTypeAccessMock(t)
		defer cleanup()

		mock.ExpectExec("DELETE FROM t_connector_type WHERE f_type = ?").
			WithArgs("remote-api").
			WillReturnError(errors.New("db down"))

		err := access.DeleteByType(context.Background(), "remote-api")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "db down")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func newConnectorTypeAccessMock(t *testing.T) (*connectorTypeAccess, sqlmock.Sqlmock, func()) {
	t.Helper()

	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)

	return &connectorTypeAccess{db: db}, mock, func() {
		mock.ExpectClose()
		require.NoError(t, db.Close())
	}
}

func connectorTypeRows() *sqlmock.Rows {
	return sqlmock.NewRows(connectorTypeColumns())
}

func sampleConnectorType() *interfaces.ConnectorType {
	return &interfaces.ConnectorType{
		Type:        "remote-api",
		Name:        "Remote API",
		Tags:        []string{"tag-a", "tag-b"},
		Description: "desc",
		Mode:        interfaces.ConnectorModeRemote,
		Category:    interfaces.ConnectorCategoryAPI,
		Endpoint:    "http://remote",
		FieldConfig: map[string]interfaces.ConnectorFieldConfig{
			"token": {
				Name:      "Token",
				Type:      "string",
				Required:  true,
				Encrypted: true,
			},
		},
		Enabled: true,
	}
}
