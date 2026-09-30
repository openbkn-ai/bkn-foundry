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

// RiskTypeAccess defines the risk-type data access interface.
//
//go:generate mockgen -source risk_type_access.go -destination mock/mock_risk_type_access.go
type RiskTypeAccess interface {
	CheckRiskTypeExistByID(ctx context.Context, knID string, branch string, rtID string) (string, bool, error)
	CheckRiskTypeExistByName(ctx context.Context, knID string, branch string, rtName string) (string, bool, error)
	GetRiskTypeIdentitiesByIDsOrNames(ctx context.Context, knID string, branch string, rtIDs, rtNames []string) ([]*RiskType, error)
	CreateRiskType(ctx context.Context, tx *sql.Tx, riskType *RiskType) error
	CreateRiskTypes(ctx context.Context, tx *sql.Tx, riskTypes []*RiskType) error
	ListRiskTypes(ctx context.Context, query RiskTypesQueryParams) ([]*RiskType, error)
	GetRiskTypesTotal(ctx context.Context, query RiskTypesQueryParams) (int, error)
	GetRiskTypesByIDs(ctx context.Context, knID string, branch string, rtIDs []string) ([]*RiskType, error)
	UpdateRiskType(ctx context.Context, tx *sql.Tx, riskType *RiskType) error
	UpdateRiskTypes(ctx context.Context, tx *sql.Tx, riskTypes []*RiskType) error
	DeleteRiskTypesByIDs(ctx context.Context, tx *sql.Tx, knID string, branch string, rtIDs []string) (int64, error)
	GetAllRiskTypesByKnID(ctx context.Context, knID string, branch string) ([]*RiskType, error)
	DeleteRiskTypesByKnID(ctx context.Context, tx *sql.Tx, knID string, branch string) (int64, error)
}
