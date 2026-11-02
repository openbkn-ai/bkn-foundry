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

// TemporalRangeHTTPError 对固定条件与追加条件统一返回可操作的范围错误。
func TemporalRangeHTTPError(ctx context.Context, err error) *httperrors.HTTPError {
	var temporal *interfaces.TemporalRangeError
	if !errors.As(err, &temporal) {
		return nil
	}
	metadata := map[string]any{
		"field_type": temporal.FieldType,
		"interval":   temporal.Interval,
		"unit":       temporal.Unit,
	}
	if temporal.Field != "" {
		metadata["field"] = temporal.Field
	}
	return httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_QueryTimeOutOfRange).WithMetadata(metadata)
}
