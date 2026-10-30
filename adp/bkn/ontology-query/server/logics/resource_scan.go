// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logics

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

// resourceScanCursorTTL bounds a Vega cursor session left behind when a scan
// stops before the last page.
const resourceScanCursorTTL = 2 * time.Minute

// ScanResourceData reads a resource query page by page and hands each page to
// visit, until visit returns false or the rows run out. params.Sort must be a
// total order, or pages may repeat or skip rows.
//
// Like object reads, the first page is a stateless single read, so a scan that
// ends on its first page holds no Vega cursor session (#1937). A scan that
// continues reopens the read as a cursor, skips the page it already has and
// follows the cursor; a resource without cursor paging continues by offset.
func ScanResourceData(ctx context.Context, vba interfaces.VegaBackendAccess, resourceID string,
	params interfaces.ResourceDataQueryParams, pageSize int,
	visit func(rows []map[string]any) (bool, error)) error {
	if pageSize <= 0 {
		return fmt.Errorf("resource scan page size must be positive")
	}
	query := func(paging interfaces.ResourceDataPagingRequest) (*interfaces.DatasetQueryResponse, error) {
		pageParams := params
		if paging.Cursor != "" {
			// Vega rejects a continuation that repeats the query; the cursor
			// session already holds it.
			pageParams = interfaces.ResourceDataQueryParams{}
		}
		pageParams.Paging = paging
		resp, err := vba.QueryResourceData(ctx, resourceID, &pageParams)
		if err == nil && resp == nil {
			err = fmt.Errorf("vega resource query returned nil")
		}
		return resp, err
	}
	// visitPage reports whether the scan should read another page.
	visitPage := func(resp *interfaces.DatasetQueryResponse) (bool, error) {
		more, err := visit(resp.Entries)
		return err == nil && more && len(resp.Entries) > 0, err
	}

	resp, err := query(interfaces.ResourceDataPagingRequest{Mode: interfaces.ResourceDataPagingModeSingle, Limit: pageSize})
	if err != nil {
		return err
	}
	if more, err := visitPage(resp); err != nil || !more || len(resp.Entries) < pageSize {
		return err
	}
	read := len(resp.Entries)

	resp, err = query(interfaces.ResourceDataPagingRequest{Mode: interfaces.ResourceDataPagingModeCursor,
		Limit: pageSize, KeepAliveSec: int(resourceScanCursorTTL / time.Second)})
	if downstream, ok := interfaces.AsVegaDownstreamError(err); ok && downstream.StatusCode == http.StatusNotImplemented {
		for offset := read; ; offset += len(resp.Entries) {
			resp, err = query(interfaces.ResourceDataPagingRequest{Mode: interfaces.ResourceDataPagingModeSingle,
				Limit: pageSize, Offset: offset})
			if err != nil {
				return err
			}
			if more, err := visitPage(resp); err != nil || !more || len(resp.Entries) < pageSize {
				return err
			}
		}
	}
	if err != nil {
		return err
	}
	// resp repeats the first page; the rows after it start at its cursor.
	for resp.Paging != nil && resp.Paging.NextCursor != nil {
		resp, err = query(interfaces.ResourceDataPagingRequest{Cursor: *resp.Paging.NextCursor})
		if err != nil {
			return err
		}
		if more, err := visitPage(resp); err != nil || !more {
			return err
		}
	}
	return nil
}
