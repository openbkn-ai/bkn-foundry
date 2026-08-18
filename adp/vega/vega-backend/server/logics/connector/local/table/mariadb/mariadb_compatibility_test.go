// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mariadb

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseMariaDBCompatibility(t *testing.T) {
	tests := []struct {
		name           string
		version        string
		versionComment string
		wantProduct    mariadbProduct
		wantVersion    int
		wantError      string
	}{
		{name: "MySQL 5.7", version: "5.7.44", versionComment: "MySQL Community Server", wantProduct: mariadbProductMySQL, wantVersion: 50744},
		{name: "rejects MySQL 5.6", version: "5.6.51", versionComment: "MySQL Community Server", wantError: "require MySQL 5.7+"},
		{name: "MariaDB 10.5", version: "10.5.27-MariaDB-0+deb11u1", versionComment: "Debian 11", wantProduct: mariadbProductMariaDB, wantVersion: 100527},
		{name: "MariaDB compatibility prefix", version: "5.5.5-10.5.27-MariaDB", versionComment: "MariaDB Server", wantProduct: mariadbProductMariaDB, wantVersion: 100527},
		{name: "rejects MariaDB 10.4", version: "10.4.34-MariaDB", versionComment: "MariaDB Server", wantError: "require MariaDB 10.5+"},
		{name: "rejects malformed version", version: "development", versionComment: "MySQL", wantError: "failed to parse database version"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compatibility, err := parseMariaDBCompatibility(tt.version, tt.versionComment)
			if err == nil {
				err = compatibility.validateMinimum()
			}
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantProduct, compatibility.product)
			assert.Equal(t, tt.wantVersion, compatibility.serverVersionNum)
			assert.True(t, compatibility.checked)
		})
	}
}

func TestFetchMariaDBCompatibility(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery(`SELECT VERSION\(\), @@version_comment`).
		WillReturnRows(sqlmock.NewRows([]string{"VERSION()", "@@version_comment"}).
			AddRow("10.5.27-MariaDB", "MariaDB Server"))

	compatibility, err := fetchMariaDBCompatibility(context.Background(), db)

	require.NoError(t, err)
	assert.Equal(t, mariadbProductMariaDB, compatibility.product)
	assert.Equal(t, 100527, compatibility.serverVersionNum)
	require.NoError(t, mock.ExpectationsWereMet())
}
