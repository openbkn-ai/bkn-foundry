// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package drivenadapters

import (
	"context"
	"fmt"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/mocks"
)

func TestAppKeyVerify(t *testing.T) {
	convey.Convey("appKeyVerifier.Verify", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		logger := mocks.NewMockLogger(ctrl)
		httpClient := mocks.NewMockHTTPClient(ctrl)
		logger.EXPECT().WithContext(gomock.Any()).Return(logger).AnyTimes()

		v := &appKeyVerifier{
			introspectURL: "http://bkn-safe:3000/api/safe/v1/api-keys/introspect",
			logger:        logger,
			httpClient:    httpClient,
		}

		convey.Convey("HTTP error -> error", func() {
			logger.EXPECT().Error(gomock.Any()).Return()
			httpClient.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(0, nil, fmt.Errorf("connection error"))

			_, err := v.Verify(context.Background(), "bak_x_y")
			convey.So(err, convey.ShouldNotBeNil)
		})

		convey.Convey("decode error -> error", func() {
			logger.EXPECT().Warnf(gomock.Any(), gomock.Any(), gomock.Any()).Return()
			httpClient.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(200, "not-an-object", nil)

			_, err := v.Verify(context.Background(), "bak_x_y")
			convey.So(err, convey.ShouldNotBeNil)
		})

		convey.Convey("inactive -> error", func() {
			httpClient.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(200, &appKeyIntrospectResp{Active: false}, nil)

			_, err := v.Verify(context.Background(), "bak_x_y")
			convey.So(err, convey.ShouldNotBeNil)
		})

		convey.Convey("active user -> RealName/user identity", func() {
			httpClient.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(200, &appKeyIntrospectResp{
					Active:      true,
					Sub:         "owner-1",
					AccountType: "other",
					KeyID:       "kid-1",
				}, nil)

			info, err := v.Verify(context.Background(), "bak_x_y")
			convey.So(err, convey.ShouldBeNil)
			convey.So(info.Active, convey.ShouldBeTrue)
			convey.So(info.VisitorID, convey.ShouldEqual, "owner-1")
			convey.So(info.VisitorTyp, convey.ShouldEqual, interfaces.RealName)
			convey.So(info.VisitorTyp.ToAccessorType(), convey.ShouldEqual, interfaces.AccessorTypeUser)
		})

		convey.Convey("app account -> Business identity", func() {
			httpClient.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(200, &appKeyIntrospectResp{Active: true, Sub: "app-1", AccountType: "app"}, nil)

			info, err := v.Verify(context.Background(), "bak_x_y")
			convey.So(err, convey.ShouldBeNil)
			convey.So(info.VisitorTyp, convey.ShouldEqual, interfaces.Business)
			convey.So(info.VisitorTyp.ToAccessorType(), convey.ShouldEqual, interfaces.AccessorTypeApp)
		})
	})
}
