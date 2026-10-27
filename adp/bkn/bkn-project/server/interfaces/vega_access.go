// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "context"

// VegaTable is a table resource of vega-backend with the structure discovery recorded.
type VegaTable struct {
	ID          string
	CatalogID   string
	Name        string
	Description string
	// SourceName and SourceSchema name the physical table; foreign keys refer to it by them.
	SourceName   string
	SourceSchema string
	// Rows is the exact row count, or the source's estimate when discovery did not count.
	Rows        int64
	RowsCounted bool
	Columns     []VegaColumn
	PrimaryKeys []string
	Indices     []VegaIndex
	ForeignKeys []VegaForeignKey
}

type VegaColumn struct {
	Name string
	// DisplayName is the business name the catalog's semantic understanding gave the field,
	// empty when it gave none.
	DisplayName string
	// Type is vega's type, close to BKN's data types.
	Type string
	// Description is the field's description from the catalog's semantic understanding, else
	// the database comment.
	Description string
	// Comment is the database comment; privacy rules read it even when semantic understanding
	// gave a description.
	Comment  string
	Nullable bool
}

type VegaIndex struct {
	Columns []string
	Unique  bool
	Primary bool
}

type VegaForeignKey struct {
	Columns    []string
	RefTable   string
	RefColumns []string
}

// VegaAccount is the account vega-backend's internal routes act for; it applies that
// account's permissions.
type VegaAccount = AccountInfo

//go:generate mockgen -source ../interfaces/vega_access.go -destination ../interfaces/mock/mock_vega_access.go
type VegaAccess interface {
	// MissingCatalogs returns the catalogs that no longer exist, in their order.
	MissingCatalogs(ctx context.Context, account VegaAccount, catalogIDs []string) ([]string, error)
	// ListTables returns the table resources of the catalog that the account can see.
	ListTables(ctx context.Context, account VegaAccount, catalogID string) ([]*VegaTable, error)
	// GetTables returns the tables with their structure; ids the account cannot see are left out.
	GetTables(ctx context.Context, account VegaAccount, ids []string) ([]*VegaTable, error)
	// Query runs a SQL statement whose tables are written {{<resource id>}} and returns its rows.
	Query(ctx context.Context, account VegaAccount, sql string, timeoutSeconds int) ([]map[string]any, error)
}
