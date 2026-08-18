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

func TestDetectPostgresqlCompatibility(t *testing.T) {
	tests := []struct {
		name                string
		lateralError        error
		withOrdinalityError error
		wantLateral         bool
		wantWithOrdinality  bool
	}{
		{
			name:               "detects both capabilities",
			wantLateral:        true,
			wantWithOrdinality: true,
		},
		{
			name:                "keeps capabilities independent",
			withOrdinalityError: errors.New("WITH ORDINALITY is unavailable"),
			wantLateral:         true,
		},
		{
			name:                "disables unsupported capabilities",
			lateralError:        errors.New("LATERAL is unavailable"),
			withOrdinalityError: errors.New("WITH ORDINALITY is unavailable"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
			require.NoError(t, err)
			t.Cleanup(func() {
				mock.ExpectClose()
				require.NoError(t, db.Close())
				require.NoError(t, mock.ExpectationsWereMet())
			})

			lateralExpectation := mock.ExpectQuery(postgresqlLateralProbe)
			if test.lateralError != nil {
				lateralExpectation.WillReturnError(test.lateralError)
			} else {
				lateralExpectation.WillReturnRows(sqlmock.NewRows([]string{"result"}).AddRow(1))
			}
			ordinalityExpectation := mock.ExpectQuery(postgresqlWithOrdinalityProbe)
			if test.withOrdinalityError != nil {
				ordinalityExpectation.WillReturnError(test.withOrdinalityError)
			} else {
				ordinalityExpectation.WillReturnRows(sqlmock.NewRows([]string{"ordinal_position"}).AddRow(1))
			}

			compatibility := detectPostgresqlCompatibility(context.Background(), db)

			assert.Equal(t, test.wantLateral, compatibility.supportsLateral())
			assert.Equal(t, test.wantWithOrdinality, compatibility.supportsWithOrdinality())
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
