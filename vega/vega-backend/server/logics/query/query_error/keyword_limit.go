// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE for the full text.

package query_error

import (
	"context"
	"errors"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	verrors "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

// KeywordLimitHTTPError 将调用方的超限错误转换为稳定错误码与安全元数据。
// 固定条件仍由视图查询层按定义失效处理。
func KeywordLimitHTTPError(ctx context.Context, err error) *rest.HTTPError {
	var stored *interfaces.StoredConditionBuildError
	if errors.As(err, &stored) {
		return nil
	}
	var limit *interfaces.KeywordLimitError
	if !errors.As(err, &limit) {
		return nil
	}
	return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_QueryValueExceedsKeywordLimit).
		WithMetadata(map[string]any{"field": limit.Field, "limit": limit.Limit})
}
