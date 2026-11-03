// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package query_error

import (
	"context"
	"errors"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	verrors "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

// DecimalIndexHTTPError 区分小数输入错误与聚合结果错误。
func DecimalIndexHTTPError(ctx context.Context, err error) *httperrors.HTTPError {
	var failure *interfaces.DecimalIndexError
	if !errors.As(err, &failure) {
		return nil
	}
	status, code := http.StatusBadRequest, verrors.VegaBackend_Resource_InvalidParameter
	if failure.Reason == "invalid_aggregate_value" {
		status, code = http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError
	}
	return httperrors.NewHTTPError(ctx, status, code).
		WithMetadata(map[string]any{"field": failure.Field, "reason": failure.Reason}).
		WithErrorDetails(failure.Error())
}
