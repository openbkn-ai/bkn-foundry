// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"context"
	"database/sql"
)

//go:generate mockgen -source ../interfaces/knowledge_network_access.go -destination ../interfaces/mock/mock_knowledge_network_access.go
type KNAccess interface {
	CheckKNExistByID(ctx context.Context, knID string, branch string) (string, bool, error)
	CheckKNExistByName(ctx context.Context, knName string, branch string) (string, bool, error)

	CreateKN(ctx context.Context, tx *sql.Tx, kn *KN) error
	ListKNs(ctx context.Context, query KNsQueryParams) ([]*KN, error)
	GetKNsTotal(ctx context.Context, query KNsQueryParams) (int, error)
	GetKNByID(ctx context.Context, knID string, branch string) (*KN, error)
	UpdateKN(ctx context.Context, tx *sql.Tx, kn *KN) error
	UpdateKNDetail(ctx context.Context, knID string, branch string, detail string) error
	DeleteKN(ctx context.Context, tx *sql.Tx, knID string, branch string) (int64, error)

	GetAllKNs(ctx context.Context) (map[string]*KN, error)
	GetNeighborPathsBatch(ctx context.Context, otIDs []string, query RelationTypePathsBaseOnSource) (map[string][]RelationTypePath, error)

	// GetKNNamesByIDs 按 ID 批量查询知识网络名称(轻查询，绕过授权过滤，仅用于对象级授权页回显)。
	// 缺失的 id 略过、空 ids 返回空 entries。
	GetKNNamesByIDs(ctx context.Context, ids []string, branch string) ([]*KNNameEntry, error)

	ListKnSrcs(ctx context.Context, query KNsQueryParams) ([]PermissionResource, error)
}
