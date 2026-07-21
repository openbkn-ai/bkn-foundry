// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	QueryFormatSQL QueryFormat = "sql"
	QueryFormatDSL QueryFormat = "dsl"

	PagingModeSingle PagingMode = "single"
	PagingModeCursor PagingMode = "cursor"

	DefaultInputDialect       = "postgres"
	DefaultPageLimit          = 20
	MinCursorPageLimit        = 1
	MaxCursorPageLimit        = 10000
	DefaultCursorKeepAliveSec = 1800
	MinCursorKeepAliveSec     = 1
	MaxCursorKeepAliveSec     = 3600
)

// QueryFormat describes the representation used for the client query.
type QueryFormat string

// PagingMode describes whether a request is one-shot or cursor-paged.
type PagingMode string

// PagingRequest is used for either a first cursor request or a continuation.
// A continuation has only Cursor set; all other fields are forbidden.
type PagingRequest struct {
	Mode         PagingMode `json:"mode,omitempty"`
	Offset       int        `json:"offset,omitempty"`
	Limit        int        `json:"limit,omitempty"`
	KeepAliveSec int        `json:"keep_alive_sec,omitempty"`
	Cursor       string     `json:"cursor,omitempty"`
}

// UnmarshalJSON rejects the removed paging.size field instead of silently
// applying the default limit to old clients.
func (p *PagingRequest) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if _, ok := fields["size"]; ok {
		return fmt.Errorf("paging.size is not supported; use paging.limit")
	}
	type pagingRequest PagingRequest
	var decoded pagingRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*p = PagingRequest(decoded)
	return nil
}

// PagingResponse exposes only opaque cursor state to the client.
type PagingResponse struct {
	NextCursor *string `json:"next_cursor"`
	// ExpiresAtSec is a Unix timestamp in seconds. It is nil when there is no
	// valid cursor to continue, such as on the final page.
	ExpiresAtSec *int64 `json:"expires_at_sec"`
}

// RawQueryContract is the replacement public request model. It is introduced
// before the legacy handler is switched so its validation can be reviewed and
// exercised independently.
type RawQueryContract struct {
	Query        any           `json:"query,omitempty"`
	QueryFormat  QueryFormat   `json:"query_format,omitempty"`
	InputDialect string        `json:"input_dialect,omitempty"`
	Paging       PagingRequest `json:"paging,omitempty"`
}

// IsContinuation reports whether the request contains a cursor continuation.
func (r RawQueryContract) IsContinuation() bool {
	return r.Paging.Cursor != ""
}

// EffectiveInputDialect applies the SQL-only default defined by the contract.
func (r RawQueryContract) EffectiveInputDialect() string {
	if r.QueryFormat == QueryFormatSQL && r.InputDialect == "" {
		return DefaultInputDialect
	}
	return strings.ToLower(r.InputDialect)
}

// Validate checks the mutually exclusive first-page and continuation forms.
func (r RawQueryContract) Validate() error {
	if r.IsContinuation() {
		if r.Query != nil || r.QueryFormat != "" || r.InputDialect != "" || r.Paging.Mode != "" || r.Paging.Offset != 0 || r.Paging.Limit != 0 || r.Paging.KeepAliveSec != 0 {
			return fmt.Errorf("cursor continuation must contain only paging.cursor")
		}
		return nil
	}

	if r.Query == nil {
		return fmt.Errorf("query is required for an initial request")
	}
	if r.QueryFormat != QueryFormatSQL && r.QueryFormat != QueryFormatDSL {
		return fmt.Errorf("query_format must be either %q or %q", QueryFormatSQL, QueryFormatDSL)
	}
	if err := r.validateQueryShape(); err != nil {
		return err
	}
	if err := r.validateInputDialect(); err != nil {
		return err
	}

	switch r.Paging.Mode {
	case "", PagingModeSingle:
	case PagingModeCursor:
		if r.Paging.Limit == 0 {
			return fmt.Errorf("paging.limit is required for cursor paging")
		}
		if r.Paging.KeepAliveSec != 0 && (r.Paging.KeepAliveSec < MinCursorKeepAliveSec || r.Paging.KeepAliveSec > MaxCursorKeepAliveSec) {
			return fmt.Errorf("paging.keep_alive_sec must be between %d and %d when provided", MinCursorKeepAliveSec, MaxCursorKeepAliveSec)
		}
		if r.QueryFormat == QueryFormatDSL {
			query := r.Query.(map[string]any)
			sort, ok := query["sort"].([]any)
			if !ok || len(sort) == 0 {
				return fmt.Errorf("sort is required for DSL cursor paging")
			}
		}
	default:
		return fmt.Errorf("paging.mode must be either %q or %q", PagingModeSingle, PagingModeCursor)
	}
	if r.Paging.Offset < 0 {
		return fmt.Errorf("paging.offset must not be negative")
	}
	if r.Paging.Mode == PagingModeCursor && (r.Paging.Limit < MinCursorPageLimit || r.Paging.Limit > MaxCursorPageLimit) {
		return fmt.Errorf("paging.limit must be between %d and %d for cursor paging", MinCursorPageLimit, MaxCursorPageLimit)
	}

	return nil
}

func (p PagingRequest) EffectiveLimit() int {
	if p.Limit == 0 {
		return DefaultPageLimit
	}
	return p.Limit
}

func (p PagingRequest) Normalized() PagingRequest {
	if p.Mode == "" {
		p.Mode = PagingModeSingle
	}
	p.Limit = p.EffectiveLimit()
	return p
}

func (r RawQueryContract) validateQueryShape() error {
	switch r.QueryFormat {
	case QueryFormatSQL:
		query, ok := r.Query.(string)
		if !ok || strings.TrimSpace(query) == "" {
			return fmt.Errorf("query must be a non-empty SQL string")
		}
	case QueryFormatDSL:
		if _, ok := r.Query.(map[string]any); !ok {
			return fmt.Errorf("query must be a JSON object for DSL input")
		}
	}
	return nil
}

func (r RawQueryContract) validateInputDialect() error {
	dialect := r.EffectiveInputDialect()
	switch r.QueryFormat {
	case QueryFormatSQL:
		switch dialect {
		case "postgres", "mysql", "trino", "duckdb":
			return nil
		default:
			return fmt.Errorf("unsupported SQL input_dialect: %s", r.InputDialect)
		}
	case QueryFormatDSL:
		if dialect != "opensearch" {
			return fmt.Errorf("DSL input_dialect must be %q", "opensearch")
		}
	}
	return nil
}
