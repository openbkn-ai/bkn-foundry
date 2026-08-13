// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package filter_condition

import (
	"fmt"
	"strings"
	"testing"
)

func Test_UnsupportedOperationError(t *testing.T) {
	t.Run("SQL 通道上的全文算子给出建索引与替代算子两条出路", func(t *testing.T) {
		err := NewUnsupportedOperationError(OperationMatch, QueryChannelSQL)
		msg := err.Error()
		// 只说「不支持」会让调用方以为这个能力不存在，实际建个索引就有了。
		for _, want := range []string{"operation match is not supported", "sql", "local index", "like"} {
			if !strings.Contains(msg, want) {
				t.Fatalf("message %q should mention %q", msg, want)
			}
		}
	})

	t.Run("SQL 通道上的向量算子指向向量特性", func(t *testing.T) {
		msg := NewUnsupportedOperationError(OperationKnnVector, QueryChannelSQL).Error()
		if !strings.Contains(msg, "vector feature") {
			t.Fatalf("message %q should point at the missing vector feature", msg)
		}
	})

	t.Run("索引通道不附带建索引提示", func(t *testing.T) {
		msg := NewUnsupportedOperationError("frobnicate", QueryChannelOpenSearch).Error()
		if strings.Contains(msg, "local index") {
			t.Fatalf("an index-backed channel already has an index, message was %q", msg)
		}
	})

	t.Run("包装后仍可识别", func(t *testing.T) {
		wrapped := fmt.Errorf("failed to execute query: %w", NewUnsupportedOperationError(OperationMatch, QueryChannelSQL))
		unsupported, ok := AsUnsupportedOperationError(wrapped)
		if !ok {
			t.Fatal("the error crosses several layers wrapped, errors.As must still find it")
		}
		if unsupported.Operation != OperationMatch {
			t.Fatalf("operation lost: %q", unsupported.Operation)
		}
	})

	t.Run("IsFulltextOperation 只认全文算子", func(t *testing.T) {
		for _, op := range []string{OperationMatch, OperationMatchPhrase, OperationMultiMatch} {
			if !IsFulltextOperation(op) {
				t.Fatalf("%s is a full-text operation", op)
			}
		}
		for _, op := range []string{OperationLike, OperationContain, OperationKnnVector} {
			if IsFulltextOperation(op) {
				t.Fatalf("%s is not a full-text operation", op)
			}
		}
	})
}
