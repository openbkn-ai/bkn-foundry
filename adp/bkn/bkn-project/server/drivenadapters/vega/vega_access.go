// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package vega

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	attr "go.opentelemetry.io/otel/attribute"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

const (
	headerAccountID   = "x-account-id"
	headerAccountType = "x-account-type"

	categoryTable = "table"
	noLimit       = "-1"
	// operationTaskManage is the catalog permission to see and run its build tasks.
	operationTaskManage = "task_manage"
)

var (
	vaOnce   sync.Once
	vaAccess interfaces.VegaAccess
)

type vegaAccess struct {
	httpClient rest.HTTPClient
	baseUrl    string
}

func NewVegaAccess(appSetting *common.AppSetting) interfaces.VegaAccess {
	vaOnce.Do(func() {
		vaAccess = &vegaAccess{httpClient: common.NewHTTPClient(), baseUrl: appSetting.VegaBackendUrl}
	})
	return vaAccess
}

// resource is the part of vega-backend's resource that drafting reads.
type resource struct {
	ID          string `json:"id"`
	CatalogID   string `json:"catalog_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	APIName     string `json:"api_name"`
	Category    string `json:"category"`
	IndexConfig *struct {
		PrimaryKeyFields  []string `json:"primary_key_fields"`
		IncrementalFields []string `json:"incremental_fields"`
	} `json:"index_config"`
	LocalStatus       string     `json:"local_status"`
	IndexName         string     `json:"index_name"`
	RowCount          *int64     `json:"row_count"`
	EstimatedRowCount *int64     `json:"estimated_row_count"`
	SourceMetadata    *tableMeta `json:"source_metadata"`
	// SchemaDefinition carries vega's types of the fields; the source metadata has the
	// database's own.
	SchemaDefinition []struct {
		Name         string `json:"name"`
		DisplayName  string `json:"display_name"`
		Type         string `json:"type"`
		Description  string `json:"description"`
		OriginalName string `json:"original_name"`
		APIName      string `json:"api_name"`
	} `json:"schema_definition"`
}

// tableMeta is the table structure discovery saved as the resource's source metadata.
type tableMeta struct {
	Name        string `json:"name"`
	Schema      string `json:"schema"`
	Description string `json:"description"`
	Columns     []struct {
		Name        string `json:"name"`
		Type        string `json:"type"`
		Description string `json:"description"`
		Nullable    bool   `json:"nullable"`
	} `json:"columns"`
	PrimaryKeys []string `json:"primary_keys"`
	Indices     []struct {
		Columns []string `json:"columns"`
		Unique  bool     `json:"unique"`
		Primary bool     `json:"primary"`
	} `json:"indices"`
	ForeignKeys []struct {
		Columns    []string `json:"columns"`
		RefTable   string   `json:"ref_table"`
		RefColumns []string `json:"ref_columns"`
	} `json:"foreign_keys"`
}

func vegaError(respCode int, respBody []byte) error {
	vegaErr := &interfaces.VegaError{HTTPCode: respCode}
	_ = json.Unmarshal(respBody, vegaErr)
	return vegaErr
}

func headers(account interfaces.VegaAccount) map[string]string {
	return map[string]string{
		headerAccountID:     account.ID,
		headerAccountType:   account.Type,
		rest.ContentTypeKey: rest.ContentTypeJson,
	}
}

// MissingCatalogs calls GET /catalogs/{id,id,...} once and, when that is not 200, each
// catalog on its own: vega-backend answers the batch with the first refusal. Only 404 is a
// missing catalog. 403 is a catalog the account lacks view_detail on, or a built-in one; the
// account may still read its tables, so it is not taken for missing.
func (va *vegaAccess) MissingCatalogs(ctx context.Context, account interfaces.VegaAccount,
	catalogIDs []string) ([]string, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "MissingCatalogs")
	defer span.End()

	if len(catalogIDs) == 0 {
		return nil, nil
	}
	respCode, _, err := va.getCatalogs(ctx, account, catalogIDs)
	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http get catalogs failed")
		return nil, err
	}
	if respCode == http.StatusOK {
		oteltrace.AddHttpAttrs4Ok(span, respCode)
		return nil, nil
	}
	missing := []string{}
	for _, catalogID := range catalogIDs {
		respCode, respBody, err := va.getCatalogs(ctx, account, []string{catalogID})
		if err != nil {
			oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http get catalog failed")
			return nil, err
		}
		switch respCode {
		case http.StatusOK, http.StatusForbidden:
		case http.StatusNotFound:
			missing = append(missing, catalogID)
		default:
			oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
			return nil, vegaError(respCode, respBody)
		}
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	return missing, nil
}

func (va *vegaAccess) getCatalogs(ctx context.Context, account interfaces.VegaAccount,
	catalogIDs []string) (int, []byte, error) {
	escaped := make([]string, 0, len(catalogIDs))
	for _, id := range catalogIDs {
		escaped = append(escaped, url.PathEscape(id))
	}
	httpUrl := va.baseUrl + "/catalogs/" + strings.Join(escaped, ",")
	respCode, respBody, err := va.httpClient.GetNoUnmarshal(ctx, httpUrl, nil, headers(account))
	logger.Debugf("GetCatalogs finished, %d ids, response code is [%d]", len(catalogIDs), respCode)
	if err != nil {
		return respCode, nil, fmt.Errorf("get catalogs: %w", err)
	}
	return respCode, respBody, nil
}

// ListTables calls GET /resources?catalog_id=&category=table&limit=-1.
func (va *vegaAccess) ListTables(ctx context.Context, account interfaces.VegaAccount,
	catalogID string) ([]*interfaces.VegaTable, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "ListTables")
	defer span.End()
	span.SetAttributes(attr.Key("catalog_id").String(catalogID))

	httpUrl := va.baseUrl + "/resources"
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{HttpUrl: httpUrl, HttpMethod: http.MethodGet})

	query := url.Values{"catalog_id": {catalogID}, "category": {categoryTable}, "limit": {noLimit}}
	respCode, respBody, err := va.httpClient.GetNoUnmarshal(ctx, httpUrl, query, headers(account))
	logger.Debugf("ListTables finished, catalog_id is [%s], response code is [%d]", catalogID, respCode)
	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http list tables failed")
		return nil, fmt.Errorf("list tables of catalog %s: %w", catalogID, err)
	}
	if respCode != http.StatusOK {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
		return nil, vegaError(respCode, respBody)
	}
	list := &struct {
		Entries []*resource `json:"entries"`
	}{}
	if err := json.Unmarshal(respBody, list); err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmarshal tables failed")
		return nil, fmt.Errorf("unmarshal tables of catalog %s: %w", catalogID, err)
	}
	tables := make([]*interfaces.VegaTable, 0, len(list.Entries))
	for _, entry := range list.Entries {
		if entry.Category == categoryTable {
			tables = append(tables, toTable(entry))
		}
	}
	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return tables, nil
}

// GetTables calls GET /resources/{id,id,...}?ignore_missing=true.
func (va *vegaAccess) GetTables(ctx context.Context, account interfaces.VegaAccount,
	ids []string) ([]*interfaces.VegaTable, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetTables")
	defer span.End()

	if len(ids) == 0 {
		return []*interfaces.VegaTable{}, nil
	}
	respCode, entries, err := va.getResources(ctx, account, ids)
	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http get tables failed")
		return nil, err
	}
	// vega-backend refuses a whole batch for one table the account lacks view_detail on: each is
	// read on its own then, and those refused are left out.
	if respCode == http.StatusForbidden && len(ids) > 1 {
		entries = nil
		for _, id := range ids {
			code, one, err := va.getResources(ctx, account, []string{id})
			if err != nil {
				oteltrace.AddHttpAttrs4Error(span, code, "InternalError", "Http get table failed")
				return nil, err
			}
			entries = append(entries, one...)
		}
		respCode = http.StatusOK
	}
	if respCode == http.StatusForbidden {
		oteltrace.AddHttpAttrs4Ok(span, respCode)
		return []*interfaces.VegaTable{}, nil
	}
	tables := make([]*interfaces.VegaTable, 0, len(entries))
	for _, entry := range entries {
		tables = append(tables, toTable(entry))
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	return tables, nil
}

// getResources calls GET /resources/{ids}?ignore_missing=true. It returns the resources on 200,
// none on 403, and an error for any other reply.
func (va *vegaAccess) getResources(ctx context.Context, account interfaces.VegaAccount,
	ids []string) (int, []*resource, error) {
	escaped := make([]string, 0, len(ids))
	for _, id := range ids {
		escaped = append(escaped, url.PathEscape(id))
	}
	httpUrl := fmt.Sprintf("%s/resources/%s", va.baseUrl, strings.Join(escaped, ","))
	query := url.Values{"ignore_missing": {"true"}}
	respCode, respBody, err := va.httpClient.GetNoUnmarshal(ctx, httpUrl, query, headers(account))
	logger.Debugf("GetTables finished, %d ids, response code is [%d]", len(ids), respCode)
	if err != nil {
		return respCode, nil, fmt.Errorf("get tables: %w", err)
	}
	switch respCode {
	case http.StatusOK:
	case http.StatusForbidden:
		return respCode, nil, nil
	default:
		return respCode, nil, vegaError(respCode, respBody)
	}
	list := &struct {
		Entries []*resource `json:"entries"`
	}{}
	if err := json.Unmarshal(respBody, list); err != nil {
		return respCode, nil, fmt.Errorf("unmarshal tables: %w", err)
	}
	return respCode, list.Entries, nil
}

// ListActiveBuildTasks calls GET /build-tasks?catalog_id=&status=pending&status=running&status=stopping
// when the account may manage the catalog's tasks, which GET /catalogs/{id} tells.
func (va *vegaAccess) ListActiveBuildTasks(ctx context.Context, account interfaces.VegaAccount,
	catalogID string) ([]*interfaces.VegaBuildTask, bool, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "ListActiveBuildTasks")
	defer span.End()
	span.SetAttributes(attr.Key("catalog_id").String(catalogID))

	// vega-backend lists nothing, with 200, for a catalog the account lacks task_manage on, so the
	// permission is read first: no tasks would otherwise read as none under way.
	respCode, respBody, err := va.getCatalogs(ctx, account, []string{catalogID})
	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http get catalog failed")
		return nil, false, err
	}
	switch respCode {
	case http.StatusOK:
	case http.StatusForbidden, http.StatusNotFound:
		oteltrace.AddHttpAttrs4Ok(span, respCode)
		return nil, false, nil
	default:
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
		return nil, false, vegaError(respCode, respBody)
	}
	catalogs := &struct {
		Entries []struct {
			Operations []string `json:"operations"`
		} `json:"entries"`
	}{}
	if err := json.Unmarshal(respBody, catalogs); err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmarshal catalog failed")
		return nil, false, fmt.Errorf("unmarshal catalog %s: %w", catalogID, err)
	}
	if len(catalogs.Entries) == 0 || !slices.Contains(catalogs.Entries[0].Operations, operationTaskManage) {
		oteltrace.AddHttpAttrs4Ok(span, respCode)
		return nil, false, nil
	}

	httpUrl := va.baseUrl + "/build-tasks"
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{HttpUrl: httpUrl, HttpMethod: http.MethodGet})

	query := url.Values{"catalog_id": {catalogID}, "limit": {noLimit}, "status": {"pending", "running", "stopping"}}
	respCode, respBody, err = va.httpClient.GetNoUnmarshal(ctx, httpUrl, query, headers(account))
	logger.Debugf("ListActiveBuildTasks finished, catalog_id is [%s], response code is [%d]", catalogID, respCode)
	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http list build tasks failed")
		return nil, false, fmt.Errorf("list build tasks of catalog %s: %w", catalogID, err)
	}
	if respCode != http.StatusOK {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
		return nil, false, vegaError(respCode, respBody)
	}
	list := &struct {
		Entries []struct {
			ID         string `json:"id"`
			ResourceID string `json:"resource_id"`
			Status     string `json:"status"`
		} `json:"entries"`
	}{}
	if err := json.Unmarshal(respBody, list); err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmarshal build tasks failed")
		return nil, false, fmt.Errorf("unmarshal build tasks of catalog %s: %w", catalogID, err)
	}
	tasks := make([]*interfaces.VegaBuildTask, 0, len(list.Entries))
	for _, entry := range list.Entries {
		tasks = append(tasks, &interfaces.VegaBuildTask{ID: entry.ID, ResourceID: entry.ResourceID, Status: entry.Status})
	}
	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return tasks, true, nil
}

// CreateBuildTask calls POST /build-tasks for a full batch build; vega-backend
// starts the task itself and answers 201 with its id.
func (va *vegaAccess) CreateBuildTask(ctx context.Context, account interfaces.VegaAccount,
	resourceID string) (string, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "CreateBuildTask")
	defer span.End()

	httpUrl := va.baseUrl + "/build-tasks"
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{HttpUrl: httpUrl, HttpMethod: http.MethodPost})

	body := map[string]any{"resource_id": resourceID, "mode": "batch", "execute_type": "full"}
	respCode, respBody, err := va.httpClient.PostNoUnmarshal(ctx, httpUrl, headers(account), body)
	logger.Debugf("CreateBuildTask finished, response code is [%d]", respCode)
	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http create build task failed")
		return "", fmt.Errorf("create build task: %w", err)
	}
	if respCode != http.StatusCreated {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 201")
		return "", vegaError(respCode, respBody)
	}
	result := &struct {
		ID string `json:"id"`
	}{}
	if err := json.Unmarshal(respBody, result); err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmarshal build task failed")
		return "", fmt.Errorf("unmarshal build task: %w", err)
	}
	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return result.ID, nil
}

// Query calls POST /resources/query with a single page of results.
func (va *vegaAccess) Query(ctx context.Context, account interfaces.VegaAccount, sql string,
	timeoutSeconds int) ([]map[string]any, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Query")
	defer span.End()

	httpUrl := va.baseUrl + "/resources/query"
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{HttpUrl: httpUrl, HttpMethod: http.MethodPost})

	body := map[string]any{
		"query":             sql,
		"query_format":      "sql",
		"query_timeout_sec": timeoutSeconds,
		"paging":            map[string]any{"mode": "single", "limit": 1000},
	}
	respCode, respBody, err := va.httpClient.PostNoUnmarshal(ctx, httpUrl, headers(account), body)
	logger.Debugf("Query finished, response code is [%d]", respCode)
	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http query failed")
		return nil, fmt.Errorf("query: %w", err)
	}
	if respCode != http.StatusOK {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
		return nil, vegaError(respCode, respBody)
	}
	result := &struct {
		Entries []map[string]any `json:"entries"`
	}{}
	if err := json.Unmarshal(respBody, result); err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmarshal rows failed")
		return nil, fmt.Errorf("unmarshal rows: %w", err)
	}
	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return result.Entries, nil
}

func toTable(r *resource) *interfaces.VegaTable {
	table := &interfaces.VegaTable{
		ID:          r.ID,
		CatalogID:   r.CatalogID,
		Name:        r.Name,
		Description: r.Description,
		APIName:     r.APIName,
		Index:       interfaces.VegaTableIndex{LocalStatus: r.LocalStatus, IndexName: r.IndexName},
	}
	if r.IndexConfig != nil {
		table.Index.PrimaryKeyFields = r.IndexConfig.PrimaryKeyFields
		table.Index.IncrementalFields = r.IndexConfig.IncrementalFields
	}
	switch {
	case r.RowCount != nil:
		table.Rows, table.RowsCounted = *r.RowCount, true
	case r.EstimatedRowCount != nil:
		table.Rows = *r.EstimatedRowCount
	}
	meta := r.SourceMetadata
	if meta == nil {
		return table
	}
	table.SourceName, table.SourceSchema = meta.Name, meta.Schema
	if table.Description == "" {
		table.Description = meta.Description
	}
	// schema_definition carries vega's types and what semantic understanding wrote back:
	// a display name other than the field's own name, and a description.
	type defined struct{ typ, displayName, description, apiName string }
	definitions := map[string]defined{}
	for _, property := range r.SchemaDefinition {
		name := property.OriginalName
		if name == "" {
			name = property.Name
		}
		d := defined{typ: property.Type, description: property.Description, apiName: property.APIName}
		if property.DisplayName != name && property.DisplayName != property.Name {
			d.displayName = property.DisplayName
		}
		definitions[name] = d
	}
	for _, column := range meta.Columns {
		d := definitions[column.Name]
		description := d.description
		if description == "" {
			description = column.Description
		}
		table.Columns = append(table.Columns, interfaces.VegaColumn{
			Name: column.Name, DisplayName: d.displayName, Type: d.typ, Description: description,
			Comment: column.Description, APIName: d.apiName, Nullable: column.Nullable,
		})
	}
	table.PrimaryKeys = meta.PrimaryKeys
	for _, index := range meta.Indices {
		table.Indices = append(table.Indices, interfaces.VegaIndex{
			Columns: index.Columns, Unique: index.Unique, Primary: index.Primary,
		})
	}
	for _, fk := range meta.ForeignKeys {
		table.ForeignKeys = append(table.ForeignKeys, interfaces.VegaForeignKey{
			Columns: fk.Columns, RefTable: fk.RefTable, RefColumns: fk.RefColumns,
		})
	}
	return table
}
