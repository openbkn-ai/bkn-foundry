// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package sqlglot

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

func TestMapDataSourceTypeToDialect(t *testing.T) {
	cases := []struct {
		name       string
		sourceType string
		want       string
	}{
		{name: "mysql", sourceType: interfaces.ConnectorTypeMySQL, want: "mysql"},
		{name: "upper mysql", sourceType: "MYSQL", want: "mysql"},
		{name: "postgres alias", sourceType: "postgres", want: "postgres"},
		{name: "postgres connector type", sourceType: interfaces.ConnectorTypePostgreSQL, want: "postgres"},
		{name: "mariadb", sourceType: interfaces.ConnectorTypeMariaDB, want: "mysql"},
		{name: "maria alias", sourceType: "maria", want: "mysql"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := MapDataSourceTypeToDialect(tc.sourceType)

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestMapDataSourceTypeToDialectUnsupported(t *testing.T) {
	t.Run("returns error for unsupported source type", func(t *testing.T) {
		got, err := MapDataSourceTypeToDialect("oracle")

		require.Error(t, err)
		assert.Empty(t, got)
		assert.Contains(t, err.Error(), "unsupported dataSourceType: oracle")
	})
}

func TestTranspileSQL(t *testing.T) {
	t.Run("returns mapping error before invoking sqlglot", func(t *testing.T) {
		got, err := TranspileSQL(context.Background(), "select * from t", "mysql", "oracle")

		require.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "unsupported dataSourceType: oracle")
	})

	t.Run("honors canceled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		got, err := TranspileSQL(ctx, "select * from t", "mysql", "postgres")

		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
		assert.Nil(t, got)
	})
}
