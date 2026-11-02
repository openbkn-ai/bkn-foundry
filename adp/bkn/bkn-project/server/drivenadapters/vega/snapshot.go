// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package vega

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

var errIncompleteSnapshot = errors.New("a complete source snapshot could not be verified")

func (va *vegaAccess) QuerySnapshot(ctx context.Context, account interfaces.VegaAccount, sql string, timeoutSeconds, maxRows int, guard func(context.Context) error) ([]map[string]any, error) {
	if guard == nil || account.ID == "" || (account.Type != "user" && account.Type != "app") || strings.TrimSpace(sql) == "" || timeoutSeconds < 1 || timeoutSeconds > 60 || maxRows < 1 || maxRows > 100000 {
		return nil, errIncompleteSnapshot
	}
	ctx, cancel := context.WithTimeout(ctx, 180*time.Second)
	defer cancel()
	body := map[string]any{"query": sql, "query_format": "sql", "query_timeout_sec": timeoutSeconds, "paging": map[string]any{"mode": "cursor", "limit": min(1000, maxRows+1), "keep_alive_sec": 60}}
	endpoint := va.baseUrl + "/resources/query"
	result := []map[string]any{}
	cursor := ""
	defer func() {
		if cursor != "" {
			cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer stop()
			_, _, _ = va.httpClient.DeleteNoUnmarshal(cleanup, va.baseUrl+"/resources/cursors/"+url.PathEscape(cursor), headers(account))
		}
	}()
	for {
		if err := guard(ctx); err != nil {
			return nil, err
		}
		code, raw, err := va.httpClient.PostNoUnmarshal(ctx, endpoint, headers(account), body)
		if err != nil {
			return nil, err
		}
		if code != http.StatusOK {
			return nil, vegaError(code, raw)
		}
		if len(raw) > 8<<20 {
			return nil, errIncompleteSnapshot
		}
		var page struct {
			Entries []map[string]any `json:"entries"`
			Paging  *struct {
				NextCursor json.RawMessage `json:"next_cursor"`
			} `json:"paging"`
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if decoder.Decode(&page) != nil || page.Entries == nil || page.Paging == nil {
			return nil, errIncompleteSnapshot
		}
		var next *string
		if json.Unmarshal(page.Paging.NextCursor, &next) != nil {
			return nil, errIncompleteSnapshot
		}
		if next != nil && *next != "" {
			cursor = *next
			if len(cursor) > 4096 || strings.TrimSpace(cursor) == "" || len(page.Entries) == 0 {
				return nil, errIncompleteSnapshot
			}
		}
		result = append(result, page.Entries...)
		if len(result) > maxRows {
			return nil, errIncompleteSnapshot
		}
		if next == nil || *next == "" {
			break
		}
		// Vega advances the session behind a stable opaque cursor. Continuations
		// must contain no query or paging options other than this cursor.
		body = map[string]any{"paging": map[string]any{"cursor": cursor}}
	}
	if err := guard(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

var _ interfaces.VegaSnapshotAccess = (*vegaAccess)(nil)
