// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"fmt"
	"strings"
)

const (
	QueryFormatSQL QueryFormat = "sql"
	QueryFormatDSL QueryFormat = "dsl"

	PagingModeSingle PagingMode = "single"
	PagingModeCursor PagingMode = "cursor"

	DefaultInputDialect       = "postgres"
	MinCursorPageSize         = 100
	MaxCursorPageSize         = 10000
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
	Size         int        `json:"size,omitempty"`
	KeepAliveSec int        `json:"keep_alive_sec,omitempty"`
	Cursor       string     `json:"cursor,omitempty"`
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
		if r.Query != nil || r.QueryFormat != "" || r.InputDialect != "" || r.Paging.Mode != "" || r.Paging.Size != 0 || r.Paging.KeepAliveSec != 0 {
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
		if r.Paging.Size != 0 || r.Paging.KeepAliveSec != 0 {
			return fmt.Errorf("paging.size and paging.keep_alive_sec are only allowed when paging.mode is %q", PagingModeCursor)
		}
	case PagingModeCursor:
		if r.Paging.Size < MinCursorPageSize || r.Paging.Size > MaxCursorPageSize {
			return fmt.Errorf("paging.size must be between %d and %d for cursor paging", MinCursorPageSize, MaxCursorPageSize)
		}
		if r.Paging.KeepAliveSec != 0 && (r.Paging.KeepAliveSec < MinCursorKeepAliveSec || r.Paging.KeepAliveSec > MaxCursorKeepAliveSec) {
			return fmt.Errorf("paging.keep_alive_sec must be between %d and %d when provided", MinCursorKeepAliveSec, MaxCursorKeepAliveSec)
		}
	default:
		return fmt.Errorf("paging.mode must be either %q or %q", PagingModeSingle, PagingModeCursor)
	}

	return nil
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
