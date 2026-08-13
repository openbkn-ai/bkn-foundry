// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package queryerr

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"vega-backend/logics/filter_condition"
)

func Test_AsHTTPError(t *testing.T) {
	ctx := context.Background()

	t.Run("算子不支持映射成 400 并保留可执行提示", func(t *testing.T) {
		err := filter_condition.NewUnsupportedOperationError(filter_condition.OperationMatch, filter_condition.QueryChannelSQL)
		httpErr, ok := AsHTTPError(ctx, err)
		if !ok {
			t.Fatal("an unsupported operation is a caller-side problem and must map to an HTTP error")
		}
		if httpErr.HTTPCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", httpErr.HTTPCode)
		}
		details, _ := httpErr.BaseError.ErrorDetails.(string)
		if !strings.Contains(details, "local index") {
			t.Fatalf("details %q should tell the caller an index is what unlocks match", details)
		}
	})

	t.Run("包装过的错误仍能被识别", func(t *testing.T) {
		wrapped := fmt.Errorf("failed to execute query: %w",
			filter_condition.NewUnsupportedOperationError(filter_condition.OperationKnnVector, filter_condition.QueryChannelSQL))
		if _, ok := AsHTTPError(ctx, wrapped); !ok {
			t.Fatal("the error travels wrapped through several layers, errors.As must still find it")
		}
	})

	t.Run("其他错误不被改判", func(t *testing.T) {
		if _, ok := AsHTTPError(ctx, fmt.Errorf("connection refused")); ok {
			t.Fatal("a genuine execution failure must stay a 500")
		}
	})
}
