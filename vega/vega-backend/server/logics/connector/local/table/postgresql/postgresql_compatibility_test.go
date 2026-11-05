// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package postgresql

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/lib/pq/pqerror"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectPostgresqlCompatibility(t *testing.T) {
	t.Run("canceled context does not execute probes", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		compatibility, err := detectPostgresqlCompatibility(ctx, db)
		require.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, postgresqlCompatibility{}, compatibility)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	for _, tc := range []struct {
		name                                 string
		lateralErr, ordinalityErr, moneyErr  error
		moneyValue                           driver.Value
		wantLateral, wantOrdinality, wantErr bool
		wantScale                            int64
	}{
		{name: "detects all definitions", moneyValue: int64(2), wantLateral: true, wantOrdinality: true, wantScale: 2},
		{name: "unsupported ordinality retains other capabilities", ordinalityErr: &pq.Error{Code: pqerror.FeatureNotSupported}, moneyValue: int64(3), wantLateral: true, wantScale: 3},
		{name: "unsupported syntax and wrapped function errors allow fallback", lateralErr: &pq.Error{Code: pqerror.SyntaxError}, ordinalityErr: fmt.Errorf("source: %w", &pq.Error{Code: pqerror.UndefinedFunction}), moneyValue: int64(0)},
		{name: "maximum money scale", moneyValue: int64(10), wantLateral: true, wantOrdinality: true, wantScale: 10},
		{name: "money query failure", moneyErr: errors.New("money definition unavailable"), wantErr: true},
		{name: "null money scale", moneyValue: nil, wantErr: true},
		{name: "negative money scale", moneyValue: int64(-1), wantErr: true},
		{name: "invalid positive money scale", moneyValue: int64(11), wantErr: true},
		{name: "unsupported money functions are fatal", moneyErr: &pq.Error{Code: pqerror.UndefinedFunction}, wantErr: true},
		{name: "lateral connection failure is fatal", lateralErr: &pq.Error{Code: pqerror.ConnectionFailure}, wantErr: true},
		{name: "ordinality permission failure is fatal", ordinalityErr: &pq.Error{Code: pqerror.InsufficientPrivilege}, wantErr: true},
		{name: "unclassified probe failure is fatal", lateralErr: errors.New("source unavailable"), wantErr: true},
		{name: "cancellation is propagated", lateralErr: context.Canceled, wantErr: true},
		{name: "deadline is propagated", ordinalityErr: context.DeadlineExceeded, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
			require.NoError(t, err)
			t.Cleanup(func() {
				mock.ExpectClose()
				require.NoError(t, db.Close())
				require.NoError(t, mock.ExpectationsWereMet())
			})
			var cause error
			for _, probe := range []struct {
				query string
				err   error
				value driver.Value
			}{
				{postgresqlLateralProbe, tc.lateralErr, int64(1)},
				{postgresqlWithOrdinalityProbe, tc.ordinalityErr, int64(1)},
				{postgresqlMoneyScaleProbe, tc.moneyErr, tc.moneyValue},
			} {
				expectedQuery := probe.query
				if probe.query == postgresqlMoneyScaleProbe {
					// 9.3 也能通过 numeric 的定点文本读取 scale，无需较新的 scale(numeric)。
					expectedQuery = "SELECT pg_catalog.length(pg_catalog.split_part(CAST(CAST(CAST(0 AS money) AS numeric) AS text), '.', 2))"
				}
				query := mock.ExpectQuery(expectedQuery)
				if probe.err != nil {
					query.WillReturnError(probe.err)
					if tc.wantErr {
						cause = probe.err
						break
					}
				} else {
					query.WillReturnRows(sqlmock.NewRows([]string{"result"}).AddRow(probe.value))
				}
			}
			compatibility, err := detectPostgresqlCompatibility(t.Context(), db)
			if tc.wantErr {
				require.Error(t, err)
				assert.Equal(t, postgresqlCompatibility{}, compatibility)
				if cause != nil {
					assert.ErrorIs(t, err, cause)
				}
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.wantLateral, compatibility.supportsLateral())
				assert.Equal(t, tc.wantOrdinality, compatibility.supportsWithOrdinality())
				assert.True(t, compatibility.moneyScale.Valid)
				assert.Equal(t, tc.wantScale, compatibility.moneyScale.Int64)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
