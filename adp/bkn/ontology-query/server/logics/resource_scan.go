// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logics

import (
	"context"
	"fmt"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

// resourceScanCursorTTL bounds a Vega cursor session left behind when a scan
// stops before the last page.
const resourceScanCursorTTL = 2 * time.Minute

type resourceDataCursorCloser interface {
	CloseResourceDataCursor(ctx context.Context, resourceID, cursor string) error
}

// CloseResourceDataCursor releases a live scan cursor using its query identity.
func CloseResourceDataCursor(ctx context.Context, vba interfaces.VegaBackendAccess, resourceID, cursor string) {
	closer, ok := vba.(resourceDataCursorCloser)
	if !ok || cursor == "" {
		return
	}
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if err := closer.CloseResourceDataCursor(closeCtx, resourceID, cursor); err != nil {
		logger.Warnf("Failed to close Vega resource cursor after scan")
	}
}

// ScanResourceData reads a resource query page by page and hands each page to
// visit, until visit returns false or Vega sends no continuation. Vega chooses
// the batch size for the resource.
func ScanResourceData(ctx context.Context, vba interfaces.VegaBackendAccess, resourceID string,
	params interfaces.ResourceDataQueryParams,
	visit func(rows []map[string]any) (bool, error)) error {
	var nextCursor string
	defer func() { CloseResourceDataCursor(ctx, vba, resourceID, nextCursor) }()
	params.Paging = interfaces.ResourceDataPagingRequest{
		Mode: interfaces.ResourceDataPagingModeCursor, KeepAliveSec: int(resourceScanCursorTTL / time.Second),
	}
	request := &params
	for {
		resp, err := vba.QueryResourceData(ctx, resourceID, request)
		if err != nil {
			return err
		}
		if resp == nil || resp.Paging == nil {
			return fmt.Errorf("vega resource query returned no paging response")
		}
		nextCursor = ""
		if resp.Paging.NextCursor != nil {
			nextCursor = *resp.Paging.NextCursor
		}
		if more, err := visit(resp.Entries); err != nil || !more || nextCursor == "" {
			return err
		}
		request = &interfaces.ResourceDataQueryParams{Paging: interfaces.ResourceDataPagingRequest{Cursor: nextCursor}}
	}
}
