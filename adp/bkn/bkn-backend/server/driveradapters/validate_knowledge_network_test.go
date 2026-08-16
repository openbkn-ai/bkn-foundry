// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	. "github.com/smartystreets/goconvey/convey"

	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
)

func Test_ValidateKN(t *testing.T) {
	Convey("Test ValidateKN\n", t, func() {
		ctx := context.Background()

		Convey("Success with valid KN\n", func() {
			kn := &interfaces.KN{
				KNID:   "kn1",
				KNName: "knowledge_network1",
				Branch: interfaces.MAIN_BRANCH,
			}
			err := ValidateKN(ctx, kn)
			So(err, ShouldBeNil)
		})

		Convey("Failed with invalid ID\n", func() {
			kn := &interfaces.KN{
				KNID:   "_invalid_id",
				KNName: "knowledge_network1",
				Branch: interfaces.MAIN_BRANCH,
			}
			err := ValidateKN(ctx, kn)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed with empty name\n", func() {
			kn := &interfaces.KN{
				KNID:   "kn1",
				KNName: "",
				Branch: interfaces.MAIN_BRANCH,
			}
			err := ValidateKN(ctx, kn)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed with empty branch\n", func() {
			kn := &interfaces.KN{
				KNID:   "kn1",
				KNName: "knowledge_network1",
				Branch: "",
			}
			err := ValidateKN(ctx, kn)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.BaseError.ErrorCode, ShouldEqual, berrors.BknBackend_KnowledgeNetwork_NullParameter_Branch)
		})
	})
}

func Test_ValidateRelationTypePathsQuery(t *testing.T) {
	Convey("Test ValidateRelationTypePathsQuery\n", t, func() {
		ctx := context.Background()

		Convey("Success with valid query\n", func() {
			query := &interfaces.RelationTypePathsBaseOnSource{
				SourceObjecTypeId: "ot1",
				Direction:         interfaces.DIRECTION_FORWARD,
				PathLength:        2,
			}
			err := ValidateRelationTypePathsQuery(ctx, query)
			So(err, ShouldBeNil)
		})

		Convey("Failed with empty source object type ID\n", func() {
			query := &interfaces.RelationTypePathsBaseOnSource{
				SourceObjecTypeId: "",
				Direction:         interfaces.DIRECTION_FORWARD,
				PathLength:        2,
			}
			err := ValidateRelationTypePathsQuery(ctx, query)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.BaseError.ErrorCode, ShouldEqual, berrors.BknBackend_KnowledgeNetwork_NullParameter_SourceObjectTypeId)
		})

		Convey("Failed with empty direction\n", func() {
			query := &interfaces.RelationTypePathsBaseOnSource{
				SourceObjecTypeId: "ot1",
				Direction:         "",
				PathLength:        2,
			}
			err := ValidateRelationTypePathsQuery(ctx, query)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.BaseError.ErrorCode, ShouldEqual, berrors.BknBackend_KnowledgeNetwork_NullParameter_Direction)
		})

		Convey("Failed with invalid direction\n", func() {
			query := &interfaces.RelationTypePathsBaseOnSource{
				SourceObjecTypeId: "ot1",
				Direction:         "invalid_direction",
				PathLength:        2,
			}
			err := ValidateRelationTypePathsQuery(ctx, query)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed with invalid path length\n", func() {
			query := &interfaces.RelationTypePathsBaseOnSource{
				SourceObjecTypeId: "ot1",
				Direction:         interfaces.DIRECTION_FORWARD,
				PathLength:        4,
			}
			err := ValidateRelationTypePathsQuery(ctx, query)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed with path length less than 1\n", func() {
			query := &interfaces.RelationTypePathsBaseOnSource{
				SourceObjecTypeId: "ot1",
				Direction:         interfaces.DIRECTION_FORWARD,
				PathLength:        0,
			}
			err := ValidateRelationTypePathsQuery(ctx, query)
			So(err, ShouldNotBeNil)
		})
	})
}

func TestKnowledgeNetworkValidationDetailsRespectLanguage(t *testing.T) {
	testCases := []struct {
		name     string
		language string
		want     string
	}{
		{"English", rest.AmericanEnglish, "branch is required."},
		{"SimplifiedChinese", rest.SimplifiedChinese, "必须提供 branch。"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := ValidateKN(
				rest.WithLanguage(context.Background(), testCase.language),
				&interfaces.KN{KNID: "kn-1", KNName: "network"},
			)
			if err == nil {
				t.Fatal("ValidateKN() error = nil, want missing branch error")
			}
			httpErr := err.(*rest.HTTPError)
			if got, ok := httpErr.BaseError.ErrorDetails.(string); !ok || got != testCase.want {
				t.Fatalf("error_details = %#v, want %q", httpErr.BaseError.ErrorDetails, testCase.want)
			}
		})
	}
}
