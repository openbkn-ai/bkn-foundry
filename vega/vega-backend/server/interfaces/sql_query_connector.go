// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"
	"database/sql"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/sqlbuilder"
)

//go:generate mockgen -source ../interfaces/sql_query_connector.go -destination ../interfaces/mock/mock_sql_query_connector.go

// SQLBuilderExecutor executes authorized SQL Builder queries using the source dialect.
// The query service owns the connector lifetime and any Rows returned by OpenSQLBuilder.
type SQLBuilderExecutor interface {
	// ExecuteSQLBuilder generates SQL and bound parameters, executes the query,
	// and reads the complete result before closing its Rows. TotalCount is the
	// number of returned rows; independent COUNT queries are owned by the service.
	ExecuteSQLBuilder(context.Context, *sqlbuilder.SelectBuilder) (*RawQueryResponse, error)

	// OpenSQLBuilder generates SQL and bound parameters, executes the query once,
	// and returns its column metadata without advancing Rows. On success, the
	// caller must close Rows and keep the connector open until reading finishes.
	OpenSQLBuilder(context.Context, *sqlbuilder.SelectBuilder) (*sql.Rows, []ColumnInfo, error)

	// ScanSQLBuilderRow advances Rows.Next and reads one row using the metadata
	// returned by OpenSQLBuilder. It normalizes driver values for Raw Query,
	// preserves binary bytes, and classifies source errors. It returns (nil, nil)
	// at EOF and leaves connector cleanup to the caller. Structured-query
	// binary_mode does not apply to this SQL result path.
	ScanSQLBuilderRow(*sql.Rows, []ColumnInfo) (map[string]any, error)
}
