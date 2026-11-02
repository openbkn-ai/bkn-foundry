// Copyright openbkn.ai. All rights reserved.
// Licensed under the OpenBKN License. See LICENSE in the repository root for details.

package query_error

import (
	"context"
	"errors"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	verrors "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

// ExecutionBudgetHTTPError 将完整计算的预算耗尽转换为稳定错误码和结构化预算类型。
func ExecutionBudgetHTTPError(ctx context.Context, err error) *httperrors.HTTPError {
	var budget *interfaces.QueryExecutionBudgetError
	if !errors.As(err, &budget) {
		return nil
	}
	return httperrors.NewHTTPError(ctx, http.StatusUnprocessableEntity, verrors.VegaBackend_Query_ExecutionBudgetExceeded).
		WithErrorDetails("query could not complete within its execution budget").WithMetadata(map[string]any{"budget": budget.Budget})
}
