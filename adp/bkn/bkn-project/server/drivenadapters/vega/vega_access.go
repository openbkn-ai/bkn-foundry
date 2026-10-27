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
	ID                string     `json:"id"`
	CatalogID         string     `json:"catalog_id"`
	Name              string     `json:"name"`
	Description       string     `json:"description"`
	Category          string     `json:"category"`
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

// VegaError is a reply of vega-backend that is not a success.
type VegaError struct {
	HTTPCode  int
	ErrorCode string `json:"error_code"`
	Detail    string `json:"description"`
}

func (e *VegaError) Error() string {
	return fmt.Sprintf("vega-backend returned HTTP %d: %s %s", e.HTTPCode, e.ErrorCode, e.Detail)
}

func vegaError(respCode int, respBody []byte) error {
	vegaErr := &VegaError{HTTPCode: respCode}
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

// CatalogVisible calls GET /catalogs/{id}.
func (va *vegaAccess) CatalogVisible(ctx context.Context, account interfaces.VegaAccount,
	catalogID string) (bool, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "CatalogVisible")
	defer span.End()
	span.SetAttributes(attr.Key("catalog_id").String(catalogID))

	httpUrl := va.baseUrl + "/catalogs/" + url.PathEscape(catalogID)
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{HttpUrl: httpUrl, HttpMethod: http.MethodGet})

	respCode, respBody, err := va.httpClient.GetNoUnmarshal(ctx, httpUrl, nil, headers(account))
	logger.Debugf("CatalogVisible finished, catalog_id is [%s], response code is [%d]", catalogID, respCode)
	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http get catalog failed")
		return false, fmt.Errorf("get catalog %s: %w", catalogID, err)
	}
	switch respCode {
	case http.StatusOK:
		oteltrace.AddHttpAttrs4Ok(span, respCode)
		return true, nil
	// 404 for a catalog that is gone or the account cannot see, 403 for a built-in one.
	case http.StatusNotFound, http.StatusForbidden:
		oteltrace.AddHttpAttrs4Ok(span, respCode)
		return false, nil
	}
	oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
	return false, vegaError(respCode, respBody)
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
	escaped := make([]string, 0, len(ids))
	for _, id := range ids {
		escaped = append(escaped, url.PathEscape(id))
	}
	httpUrl := fmt.Sprintf("%s/resources/%s", va.baseUrl, strings.Join(escaped, ","))
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{HttpUrl: httpUrl, HttpMethod: http.MethodGet})

	query := url.Values{"ignore_missing": {"true"}}
	respCode, respBody, err := va.httpClient.GetNoUnmarshal(ctx, httpUrl, query, headers(account))
	logger.Debugf("GetTables finished, %d ids, response code is [%d]", len(ids), respCode)
	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http get tables failed")
		return nil, fmt.Errorf("get tables: %w", err)
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
		return nil, fmt.Errorf("unmarshal tables: %w", err)
	}
	tables := make([]*interfaces.VegaTable, 0, len(list.Entries))
	for _, entry := range list.Entries {
		tables = append(tables, toTable(entry))
	}
	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return tables, nil
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
	type defined struct{ typ, displayName, description string }
	definitions := map[string]defined{}
	for _, property := range r.SchemaDefinition {
		name := property.OriginalName
		if name == "" {
			name = property.Name
		}
		d := defined{typ: property.Type, description: property.Description}
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
			Comment: column.Description, Nullable: column.Nullable,
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
