// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"
	"errors"
)

// ErrQueryPageSessionExpired means the source snapshot can no longer be read.
var ErrQueryPageSessionExpired = errors.New("query page session expired")

//go:generate mockgen -source ../interfaces/query_page_session.go -destination ../interfaces/mock/mock_query_page_session.go

// QueryPageSession keeps one source snapshot for successive pages. The caller
// serializes reads and closes the session when the pages are no longer needed.
type QueryPageSession interface {
	ReadPage(context.Context, int) (*QueryPage, error)
	Close() error
}

// QueryPage is one page read from a source snapshot.
type QueryPage struct {
	Columns    []ColumnInfo
	Entries    []map[string]any
	TotalCount *int64
	HasMore    bool
}
