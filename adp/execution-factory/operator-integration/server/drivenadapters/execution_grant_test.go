// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

func TestExecutionCredentialVerify(t *testing.T) {
	Convey("An execution credential is verified by bkn-safe's execution introspection", t, func() {
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

		Convey("it acts for its delegator, bound to its network", func() {
			httpClient.EXPECT().Post(gomock.Any(), "http://bkn-safe:3000/api/safe/v1/execution-grants/introspect", gomock.Any(), gomock.Any()).
				Return(200, &appKeyIntrospectResp{Active: true, Sub: "user-1", AccountType: "other", DelegationID: "run-1", KnID: "kn-1"}, nil)
			info, err := v.Verify(context.Background(), "bex_run-1_secret")
			So(err, ShouldBeNil)
			So(info.VisitorID, ShouldEqual, "user-1")
			So(info.VisitorTyp, ShouldEqual, interfaces.RealName)
			So(info.DelegatedKnID, ShouldEqual, "kn-1")
			So(info.DelegationID, ShouldEqual, "run-1")
		})
		Convey("one that names no network, or acts for an application, is refused", func() {
			httpClient.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(200, &appKeyIntrospectResp{Active: true, Sub: "user-1", AccountType: "other", DelegationID: "run-1"}, nil)
			_, err := v.Verify(context.Background(), "bex_run-1_secret")
			So(err, ShouldNotBeNil)
			httpClient.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(200, &appKeyIntrospectResp{Active: true, Sub: "app-1", AccountType: "app", DelegationID: "run-1", KnID: "kn-1"}, nil)
			_, err = v.Verify(context.Background(), "bex_run-1_secret")
			So(err, ShouldNotBeNil)
		})
		Convey("an AppKey is not bound to a network", func() {
			httpClient.EXPECT().Post(gomock.Any(), "http://bkn-safe:3000/api/safe/v1/api-keys/introspect", gomock.Any(), gomock.Any()).
				Return(200, &appKeyIntrospectResp{Active: true, Sub: "user-1", AccountType: "other", KnID: "kn-1"}, nil)
			info, err := v.Verify(context.Background(), "bak_x_y")
			So(err, ShouldBeNil)
			So(info.DelegatedKnID, ShouldEqual, "")
		})
	})
}
