// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package queryerr 负责把查询执行期的错误翻译成对调用方有意义的 HTTP 错误。
//
// 单独成包是为了避开 import 环：resource_data 引用 logic_view，两边都需要这套映射。
package queryerr

import (
	"context"
	"net/http"

	verrors "vega-backend/errors"
	"vega-backend/logics/filter_condition"

	"github.com/openbkn-ai/bkn-comm-go/rest"
)

// AsHTTPError 判断执行期错误是否属于请求侧问题，是则返回对应的 HTTP 错误。
//
// 查询执行失败的绝大多数原因是真的执行不下去（连不上、超时、SQL 语法炸了），一律
// 500 没错。但「这个算子这条通道不支持」不是故障：调用方换个算子、或者给资源建索引
// 就能成功。把它报成 500「请联系管理员」，调用方只会去查服务健康，而唯一有用的那句
// 提示还埋在层层包装里。第二个返回值为 false 时调用方按原来的方式处理。
func AsHTTPError(ctx context.Context, err error) (*rest.HTTPError, bool) {
	if unsupported, ok := filter_condition.AsUnsupportedOperationError(err); ok {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
			WithErrorDetails(unsupported.Error()), true
	}
	return nil, false
}
