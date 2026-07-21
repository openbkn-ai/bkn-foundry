// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "sync"

// CursorSession is the server-owned state shared by Raw Query and Resource
// Data cursor pagination. It is intentionally not part of an HTTP payload.
type CursorSession struct {
	mu sync.Mutex

	ID          string
	AccountID   string
	CatalogID   string
	ResourceIDs []string
	CompiledSQL string

	QueryFormat     QueryFormat
	OpenSearchQuery map[string]any
	OpenSearchIndex string
	SearchAfter     []any

	ResourceDataResourceID string
	ResourceDataUpdateTime int64
	ResourceDataParams     *ResourceDataQueryParams
	ResourceDataCategory   string

	Offset int
	Limit  int

	TotalCount    int64
	HasTotalCount bool
	NeedTotal     bool

	KeepAliveSec    int
	QueryTimeoutSec int

	CreatedAtSec            int64
	LastSuccessfulPageAtSec int64
	ExpiresAtSec            int64
}

func (s *CursorSession) Lock()         { s.mu.Lock() }
func (s *CursorSession) Unlock()       { s.mu.Unlock() }
func (s *CursorSession) TryLock() bool { return s.mu.TryLock() }
