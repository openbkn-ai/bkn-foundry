// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package postgresql

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostgresqlCompatibility(t *testing.T) {
	t.Run("supports PostgreSQL 9.2 with legacy metadata queries", func(t *testing.T) {
		compatibility := postgresqlCompatibility{serverVersionNum: 90204, checked: true}

		require.NoError(t, compatibility.validateMinimum())
		assert.False(t, compatibility.supportsLateral())
		assert.False(t, compatibility.supportsWithOrdinality())
		assert.Equal(t, []string{"r", "v", "f"}, compatibility.tableRelKinds())
	})

	t.Run("enables LATERAL and materialized views on PostgreSQL 9.3", func(t *testing.T) {
		compatibility := postgresqlCompatibility{serverVersionNum: 90300, checked: true}

		assert.True(t, compatibility.supportsLateral())
		assert.False(t, compatibility.supportsWithOrdinality())
		assert.Equal(t, []string{"r", "v", "f", "m"}, compatibility.tableRelKinds())
	})

	t.Run("enables WITH ORDINALITY on PostgreSQL 9.4", func(t *testing.T) {
		compatibility := postgresqlCompatibility{serverVersionNum: 90400, checked: true}

		assert.True(t, compatibility.supportsLateral())
		assert.True(t, compatibility.supportsWithOrdinality())
	})

	t.Run("enables partitioned tables on PostgreSQL 10", func(t *testing.T) {
		compatibility := postgresqlCompatibility{serverVersionNum: 100000, checked: true}

		assert.Equal(t, []string{"r", "v", "f", "m", "p"}, compatibility.tableRelKinds())
	})

	t.Run("rejects versions before PostgreSQL 9.2", func(t *testing.T) {
		compatibility := postgresqlCompatibility{serverVersionNum: 90124, checked: true}

		err := compatibility.validateMinimum()

		require.ErrorContains(t, err, "PostgreSQL 9.1.24 is not supported; require PostgreSQL 9.2+")
	})
}

func TestFetchPostgresqlCompatibility(t *testing.T) {
	t.Run("reads server version number", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, mock.ExpectationsWereMet()) })
		mock.ExpectQuery(`SHOW server_version_num`).
			WillReturnRows(sqlmock.NewRows([]string{"server_version_num"}).AddRow("90204"))

		compatibility, err := fetchPostgresqlCompatibility(context.Background(), db)

		require.NoError(t, err)
		assert.Equal(t, 90204, compatibility.serverVersionNum)
		assert.True(t, compatibility.checked)
	})

	t.Run("returns version detection error", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, mock.ExpectationsWereMet()) })
		mock.ExpectQuery(`SHOW server_version_num`).WillReturnError(errors.New("version unavailable"))

		_, err = fetchPostgresqlCompatibility(context.Background(), db)

		require.ErrorContains(t, err, "version unavailable")
	})
}

func TestPostgresqlVersion(t *testing.T) {
	tests := []struct {
		name             string
		serverVersionNum int
		want             string
	}{
		{name: "legacy version with patch", serverVersionNum: 90204, want: "9.2.4"},
		{name: "legacy version without patch", serverVersionNum: 90400, want: "9.4"},
		{name: "modern version", serverVersionNum: 150002, want: "15.2"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, postgresqlVersion(test.serverVersionNum))
		})
	}
}
