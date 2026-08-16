// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "context"

// RiskTypeEvalResult contains a risk assessment result.
type RiskTypeEvalResult struct {
	Allow   bool
	Message string
}

//go:generate mockgen -source risk_type_service.go -destination mock/mock_risk_type_service.go
type RiskTypeService interface {
	// Evaluate assesses the risk of an ActionType.
	Evaluate(ctx context.Context, actionType *ActionType, knID string, branch string) (*RiskTypeEvalResult, error)
	// MustAllow returns an error when risk assessment denies the action.
	MustAllow(ctx context.Context, actionType *ActionType, knID string, branch string) error
}
