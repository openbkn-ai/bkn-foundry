// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"context"
	"fmt"
)

const (
	QueryType_Standard = "standard" // Standard Query
	QueryType_Stream   = "stream"   // Stream query
)

// RawQueryRequest SQL query request
type RawQueryRequest struct {
	Query           any           `json:"query,omitempty"`
	QueryFormat     QueryFormat   `json:"query_format,omitempty"`
	InputDialect    string        `json:"input_dialect,omitempty"`
	Paging          PagingRequest `json:"paging,omitempty"`
	QueryTimeoutSec int           `json:"query_timeout_sec,omitempty"` // Query timeout time (in seconds), default 60, minimum 1, maximum 3600
	NeedTotal       bool          `json:"need_total,omitempty"`        // Whether to return the complete total

	// These fields are service-internal bindings supplied by a Logic View when
	// it delegates to Raw Query; they are never accepted from HTTP payloads.
	ResourceDataResourceID string `json:"-"`
	ResourceDataUpdateTime int64  `json:"-"`
}

func (r RawQueryRequest) Contract() RawQueryContract {
	return RawQueryContract{
		Query:        r.Query,
		QueryFormat:  r.QueryFormat,
		InputDialect: r.InputDialect,
		Paging:       r.Paging,
	}
}

func (r RawQueryRequest) IsContinuation() bool {
	return r.Contract().IsContinuation()
}

func (r RawQueryRequest) EffectiveInputDialect() string {
	return r.Contract().EffectiveInputDialect()
}

func (r RawQueryRequest) ValidateContract() error {
	if r.IsContinuation() && r.QueryTimeoutSec != 0 {
		return fmt.Errorf("query_timeout_sec is only allowed for an initial request")
	}
	return r.Contract().Validate()
}

func (r *RawQueryRequest) NormalizePaging() {
	if !r.IsContinuation() {
		r.Paging = r.Paging.Normalized()
	}
}

// RawQueryResponse SQL query response
type RawQueryResponse struct {
	Columns     []ColumnInfo     `json:"columns"`               // Column information
	Entries     []map[string]any `json:"entries"`               // Query result
	TotalCount  *int64           `json:"total_count,omitempty"` // Total number of items; "nil" indicates no request
	Warnings    []string         `json:"warnings,omitempty"`    // Non-blocking alerts (such as deprecated resource hit alerts)
	Paging      *PagingResponse  `json:"paging,omitempty"`
	SearchAfter []any            `json:"-"` // OpenSearch internal cursor state
	ResourceIDs []string         `json:"-"` // Resolved resources used by the executed query.
}

// ColumnInfo column information
type ColumnInfo struct {
	Name string `json:"name"` // "List"
	Type string `json:"type"` // Column type
}

//go:generate mockgen -source ../interfaces/query.go -destination ../interfaces/mock/mock_query.go

// RawQueryService SQL query service interface
type RawQueryService interface {
	// Execute executes SQL queries
	Execute(ctx context.Context, req *RawQueryRequest) (*RawQueryResponse, error)
}
