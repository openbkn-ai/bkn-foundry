// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"fmt"
	"testing"

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/mocks"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

func TestAppKeyVerify(t *testing.T) {
	Convey("appKeyVerifier.Verify", t, func() {
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

		Convey("bkn-safe 不可达时返回 401，不泄露内部错误", func() {
			logger.EXPECT().Errorf(gomock.Any(), gomock.Any()).Return()
			httpClient.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(0, nil, fmt.Errorf("connection refused to internal host"))

			info, err := v.Verify(context.Background(), "bak_abc")
			So(err, ShouldNotBeNil)
			So(info, ShouldBeNil)
			So(err.Error(), ShouldNotContainSubstring, "connection refused")
		})

		Convey("响应无法解析时返回错误", func() {
			logger.EXPECT().Warnf(gomock.Any(), gomock.Any(), gomock.Any()).Return()
			httpClient.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(200, "not-an-object", nil)

			info, err := v.Verify(context.Background(), "bak_abc")
			So(err, ShouldNotBeNil)
			So(info, ShouldBeNil)
		})

		Convey("active=false（Key 非法或已撤销）返回 401", func() {
			httpClient.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(200, &appKeyIntrospectResp{Active: false}, nil)

			info, err := v.Verify(context.Background(), "bak_revoked")
			So(err, ShouldNotBeNil)
			So(info, ShouldBeNil)
		})

		Convey("普通用户 Key 解析为实名访问者", func() {
			httpClient.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(200, &appKeyIntrospectResp{
					Active:      true,
					Sub:         "owner-1",
					AccountType: "other",
					KeyID:       "kid-1",
				}, nil)

			info, err := v.Verify(context.Background(), "bak_abc")
			So(err, ShouldBeNil)
			So(info.Active, ShouldBeTrue)
			So(info.VisitorID, ShouldEqual, "owner-1")
			So(info.VisitorTyp, ShouldEqual, interfaces.RealName)
			// 下游 AccountAuthContext 取的是 ToAccessorType()，必须与 OAuth 令牌路径一致
			So(info.VisitorTyp.ToAccessorType(), ShouldEqual, interfaces.AccessorTypeUser)
		})

		Convey("应用账户 Key 解析为 Business", func() {
			httpClient.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(200, &appKeyIntrospectResp{
					Active:      true,
					Sub:         "app-1",
					AccountType: "app",
				}, nil)

			info, err := v.Verify(context.Background(), "bak_app")
			So(err, ShouldBeNil)
			So(info.VisitorTyp, ShouldEqual, interfaces.Business)
		})

		Convey("id_card 账户映射为 IDCard 账户类型", func() {
			httpClient.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(200, &appKeyIntrospectResp{
					Active:      true,
					Sub:         "user-2",
					AccountType: "id_card",
				}, nil)

			info, err := v.Verify(context.Background(), "bak_idcard")
			So(err, ShouldBeNil)
			So(info.AccountTyp, ShouldEqual, interfaces.IDCard)
		})
	})
}

func TestGetToken(t *testing.T) {
	Convey("GetToken 凭据提取", t, func() {
		Convey("Authorization 头剥掉 Bearer 前缀", func() {
			c := newHydraTestContext()
			c.Request.Header.Set("Authorization", "Bearer bak_from_auth")
			So(GetToken(c), ShouldEqual, "bak_from_auth")
		})

		Convey("Authorization 为空时回落 X-Authorization", func() {
			c := newHydraTestContext()
			c.Request.Header.Set("X-Authorization", "bak_from_xauth")
			So(GetToken(c), ShouldEqual, "bak_from_xauth")
		})

		Convey("两个头都为空时回落 token 查询参数", func() {
			c := newHydraTestContext()
			c.Request.URL.RawQuery = "token=bak_from_query"
			So(GetToken(c), ShouldEqual, "bak_from_query")
		})

		Convey("无凭据时返回空串", func() {
			c := newHydraTestContext()
			So(GetToken(c), ShouldEqual, "")
		})
	})
}
