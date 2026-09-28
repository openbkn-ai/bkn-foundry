// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package filter_condition

import "testing"

func TestIsFulltextOperation(t *testing.T) {
	for _, op := range []string{OperationMatch, OperationMatchPhrase, OperationMultiMatch} {
		if !IsFulltextOperation(op) {
			t.Errorf("%s is a full-text operation", op)
		}
	}
	for _, op := range []string{OperationLike, OperationContain, OperationKnnVector} {
		if IsFulltextOperation(op) {
			t.Errorf("%s is not a full-text operation", op)
		}
	}
}
