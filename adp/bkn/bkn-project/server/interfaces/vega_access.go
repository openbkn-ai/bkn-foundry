// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"
	"fmt"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
)

// VegaTable is a table resource of vega-backend with the structure discovery recorded.
type VegaTable struct {
	ID          string
	CatalogID   string
	Name        string
	Description string
	// APIName is the English short name semantic understanding gave, empty when it gave none;
	// tables whose physical name is not ASCII take their ids from it.
	APIName string
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
	// Index is vega-backend's local index of the table, which full-text and vector search need.
	Index VegaTableIndex
}

// States of a table's local index in vega-backend.
const (
	VEGA_LOCAL_STATUS_UNAVAILABLE = "unavailable"
	VEGA_LOCAL_STATUS_AVAILABLE   = "available"
	// VEGA_LOCAL_STATUS_STALE is an index built before the table's configuration or structure
	// changed: it is no longer used.
	VEGA_LOCAL_STATUS_STALE = "stale"
)

// VegaTableIndex is what vega-backend holds of a table's local index: the fields a build needs
// configured, and the index's state.
type VegaTableIndex struct {
	PrimaryKeyFields  []string
	IncrementalFields []string
	LocalStatus       string
	IndexName         string
}

// VegaBuildTask is a task building a table's local index.
type VegaBuildTask struct {
	ID         string
	ResourceID string
	Status     string
}

// VegaIndexKeys are the fields a table's local index identifies records by and refreshes them by.
type VegaIndexKeys struct {
	PrimaryKeyFields  []string
	IncrementalFields []string
}

// How a semantic understanding task writes its result to the table.
const (
	// VEGA_SEMANTIC_APPLY_FILL_EMPTY writes only what is empty, keeping what people wrote; the default.
	VEGA_SEMANTIC_APPLY_FILL_EMPTY = "fill_empty"
	// VEGA_SEMANTIC_APPLY_FORCE overwrites names and descriptions; an api_name is never overwritten.
	VEGA_SEMANTIC_APPLY_FORCE = "force"
	// VEGA_SEMANTIC_APPLY_DRY_RUN writes nothing and only records the result.
	VEGA_SEMANTIC_APPLY_DRY_RUN = "dry_run"
)

// VegaSemanticTask is a semantic understanding task of one table: it writes the names,
// descriptions and api_names the table and its fields lack.
type VegaSemanticTask struct {
	ID         string
	ResourceID string
	// Status is pending, running, completed, failed or cancelled.
	Status string
	// Applied tells whether the task wrote its result to the table: a dry run never does, nor a
	// result below the confidence threshold.
	Applied bool
	// ApplyMode is how the task was to write its result, one of the VEGA_SEMANTIC_APPLY_* modes.
	ApplyMode  string
	CreateTime int64
	FinishTime int64
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
	Comment string
	// APIName is the English short name semantic understanding gave, empty when it gave none. Left
	// out of the JSON when empty, so draft fingerprints of tables without it stay as they were.
	APIName  string `json:",omitempty"`
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

// VegaError is a reply of vega-backend that is not a success, with its status
// and error code, so that callers can tell refusals apart.
type VegaError struct {
	HTTPCode int
	httperrors.BaseError
}

func (e *VegaError) Error() string {
	return fmt.Sprintf("vega-backend returned HTTP %d: %s %s", e.HTTPCode, e.ErrorCode, e.Description)
}

//go:generate mockgen -source ../interfaces/vega_access.go -destination ../interfaces/mock/mock_vega_access.go
type VegaAccess interface {
	// MissingCatalogs returns the catalogs that no longer exist, in their order.
	MissingCatalogs(ctx context.Context, account VegaAccount, catalogIDs []string) ([]string, error)
	// ListTables returns the table resources of the catalog that the account can see.
	ListTables(ctx context.Context, account VegaAccount, catalogID string) ([]*VegaTable, error)
	// GetTables returns the tables with their structure; ids the account cannot see are left out.
	GetTables(ctx context.Context, account VegaAccount, ids []string) ([]*VegaTable, error)
	// ListActiveBuildTasks returns the catalog's index build tasks that are pending, running or
	// stopping, and whether the account may see them: listing them needs the catalog's
	// task_manage permission.
	ListActiveBuildTasks(ctx context.Context, account VegaAccount, catalogID string) ([]*VegaBuildTask, bool, error)
	// CreateBuildTask starts a full batch build of the table's local index as the account and
	// returns the task id. A refusal is a *VegaError.
	CreateBuildTask(ctx context.Context, account VegaAccount, resourceID string) (string, error)
	// UpdateIndexConfig gives the table's index configuration the keys it lacks, as the account,
	// keeping every key it has; it tells whether it wrote anything. A refusal is a *VegaError.
	UpdateIndexConfig(ctx context.Context, account VegaAccount, resourceID string, keys VegaIndexKeys) (bool, error)
	// CreateSemanticTask starts, as the account, a semantic understanding task of the table written
	// in language (zh-CN or en-US) and applied by applyMode, one of the VEGA_SEMANTIC_APPLY_* modes;
	// empty fills only what is empty. A refusal is a *VegaError.
	CreateSemanticTask(ctx context.Context, account VegaAccount, resourceID string, language string,
		applyMode string) (*VegaSemanticTask, error)
	// ListSemanticTasks returns the semantic understanding tasks of the catalog's tables, newest
	// first, and whether the account may see them: listing them needs the catalog's task_manage
	// permission.
	ListSemanticTasks(ctx context.Context, account VegaAccount, catalogID string) ([]*VegaSemanticTask, bool, error)
	// Query runs a SQL statement whose tables are written {{<resource id>}} and returns its rows.
	Query(ctx context.Context, account VegaAccount, sql string, timeoutSeconds int) ([]map[string]any, error)
}
