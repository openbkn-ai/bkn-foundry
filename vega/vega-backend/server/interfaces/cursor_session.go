// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"sync"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/sqlbuilder"
)

// CursorSession is the server-owned state shared by Raw Query and Resource
// Data cursor pagination. It is intentionally not part of an HTTP payload.
type CursorSession struct {
	mu sync.Mutex

	ID        string
	AccountID string
	// QuotaOwnerID may be the trusted caller behind an internal access account.
	// AccountID remains the authority for cursor continuation and close.
	QuotaOwnerID string
	CatalogID    string
	ResourceIDs  []string

	// Builder 通道保存结构化查询；物理名称、目标类型及版本在每次续页前复核。
	SQLQuery            *sqlbuilder.SelectBuilder
	SQLResourceVersions map[string]int64
	SQLSourceValidator  SQLQueryCursorValidator
	CatalogUpdateTime   int64
	ConnectorType       string

	// 阅读会话保存源快照，游标只保存对会话的引用。
	ReadSession            QueryPageSession
	ReadConnector          Connector
	ReadHasMore            bool
	ReadIndexName          string
	ReadResourceUpdateTime int64

	QueryFormat     QueryFormat
	OpenSearchQuery map[string]any
	OpenSearchIndex string
	SearchAfter     []any

	ResourceDataResourceID string
	ResourceDataUpdateTime int64
	ResourceDataParams     *ResourceDataQueryParams
	ResourceDataCategory   string
	ResourceDataSource     *ResourceDataCursorSource

	// API 页码按固定页长推进，但到达总数由实际已返回行数决定。
	ResourceDataAPIPageMode bool
	ResourceDataAPIRowsRead int64

	PageOffset int
	PageLimit  int

	TotalCount *int64
	NeedTotal  bool

	KeepAliveSec    int
	QueryTimeoutSec int

	CreatedAtSec            int64
	LastSuccessfulPageAtSec int64
	ExpiresAtSec            int64
}

// ResourceDataCursorSource records the source state used by a virtual resource cursor.
type ResourceDataCursorSource struct {
	ID               string
	UpdateTime       int64
	Category         string
	LocalIndexName   string
	LocalIndexStatus string
}

func (s *CursorSession) Lock()         { s.mu.Lock() }
func (s *CursorSession) Unlock()       { s.mu.Unlock() }
func (s *CursorSession) TryLock() bool { return s.mu.TryLock() }
