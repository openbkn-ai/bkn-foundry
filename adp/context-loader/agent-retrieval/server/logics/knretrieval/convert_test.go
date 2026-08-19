// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knretrieval

import (
	"testing"

	"github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// TestParseKnOperationType_Success test ParseKnOperationType success scenario.
func TestParseKnOperationType_Success(t *testing.T) {
	convey.Convey("TestParseKnOperationType_Success", t, func() {
		testCases := []struct {
			input    string
			expected interfaces.KnOperationType
		}{
			{"and", interfaces.KnOperationTypeAnd},
			{"or", interfaces.KnOperationTypeOr},
			{"==", interfaces.KnOperationTypeEqual},
			{"!=", interfaces.KnOperationTypeNotEqual},
			{">", interfaces.KnOperationTypeGreater},
			{"<", interfaces.KnOperationTypeLess},
			{">=", interfaces.KnOperationTypeGreaterOrEqual},
			{"<=", interfaces.KnOperationTypeLessOrEqual},
			{"in", interfaces.KnOperationTypeIn},
			{"not_in", interfaces.KnOperationTypeNotIn},
			{"like", interfaces.KnOperationTypeLike},
			{"not_like", interfaces.KnOperationTypeNotLike},
			{"range", interfaces.KnOperationTypeRange},
			{"out_range", interfaces.KnOperationTypeOutRange},
			{"exist", interfaces.KnOperationTypeExist},
			{"not_exist", interfaces.KnOperationTypeNotExist},
			{"regex", interfaces.KnOperationTypeRegex},
			{"match", interfaces.KnOperationTypeMatch},
			{"knn", interfaces.KnOperationTypeKnn},
		}

		for _, tc := range testCases {
			result, err := ParseKnOperationType(tc.input)
			convey.So(err, convey.ShouldBeNil)
			convey.So(result, convey.ShouldEqual, tc.expected)
		}
	})
}

// TestParseKnOperationType_Invalid Test ParseKnOperationType invalid input.
func TestParseKnOperationType_Invalid(t *testing.T) {
	convey.Convey("TestParseKnOperationType_Invalid", t, func() {
		invalidInputs := []string{
			"invalid",
			"AND", // Case sensitive.
			"OR",
			"equals",
			"",
		}

		for _, input := range invalidInputs {
			_, err := ParseKnOperationType(input)
			convey.So(err, convey.ShouldNotBeNil)
		}
	})
}
