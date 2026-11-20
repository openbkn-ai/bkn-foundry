// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/mocks"
)

func TestExecutionCredentialVerify(t *testing.T) {
	convey.Convey("An execution credential is verified by bkn-safe's execution introspection", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		logger := mocks.NewMockLogger(ctrl)
		httpClient := mocks.NewMockHTTPClient(ctrl)
		logger.EXPECT().WithContext(gomock.Any()).Return(logger).AnyTimes()
		v := &appKeyVerifier{
			introspectURL:          "http://bkn-safe:3000/api/safe/v1/api-keys/introspect",
			executionIntrospectURL: "http://bkn-safe:3000/api/safe/v1/execution-grants/introspect",
			logger:                 logger,
			httpClient:             httpClient,
		}

		convey.Convey("it acts for its delegator, bound to its network", func() {
			httpClient.EXPECT().Post(gomock.Any(), "http://bkn-safe:3000/api/safe/v1/execution-grants/introspect", gomock.Any(), gomock.Any()).
				Return(200, &appKeyIntrospectResp{Active: true, Sub: "user-1", SubjectName: "U", AccountType: "other",
					DelegationID: "run-1", KnID: "kn-1"}, nil)
			info, err := v.Verify(context.Background(), "bex_run-1_secret")
			convey.So(err, convey.ShouldBeNil)
			convey.So(info.VisitorID, convey.ShouldEqual, "user-1")
			convey.So(info.VisitorTyp, convey.ShouldEqual, interfaces.RealName)
			convey.So(info.DelegatedKnID, convey.ShouldEqual, "kn-1")
			convey.So(info.DelegationID, convey.ShouldEqual, "run-1")
		})

		convey.Convey("one that names no network is refused", func() {
			httpClient.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(200, &appKeyIntrospectResp{Active: true, Sub: "user-1", AccountType: "other", DelegationID: "run-1"}, nil)
			_, err := v.Verify(context.Background(), "bex_run-1_secret")
			convey.So(err, convey.ShouldNotBeNil)
		})

		convey.Convey("an AppKey is not bound to a network", func() {
			httpClient.EXPECT().Post(gomock.Any(), "http://bkn-safe:3000/api/safe/v1/api-keys/introspect", gomock.Any(), gomock.Any()).
				Return(200, &appKeyIntrospectResp{Active: true, Sub: "user-1", AccountType: "other", KnID: "kn-1"}, nil)
			info, err := v.Verify(context.Background(), "bak_x_y")
			convey.So(err, convey.ShouldBeNil)
			convey.So(info.DelegatedKnID, convey.ShouldEqual, "")
		})
	})
}
