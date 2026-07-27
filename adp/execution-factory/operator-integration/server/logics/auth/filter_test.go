package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/mocks"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

func publicCtx() context.Context {
	ctx := common.SetPublicAPIToCtx(context.Background(), true)
	return common.SetAccountAuthContextToCtx(ctx, &interfaces.AccountAuthContext{
		AccountID:   "user-1",
		AccountType: interfaces.AccessorTypeUser,
	})
}

func TestFilterViewableIDs(t *testing.T) {
	Convey("FilterViewableIDs", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ids := []string{"a", "b", "c"}

		Convey("内部面原样返回，不触发判定", func() {
			authService := mocks.NewMockIAuthorizationService(ctrl)
			got, err := FilterViewableIDs(context.Background(), authService, "", ids, interfaces.AuthResourceTypeOperator)
			So(err, ShouldBeNil)
			So(got, ShouldResemble, ids)
		})

		Convey("空ID列表直接返回", func() {
			authService := mocks.NewMockIAuthorizationService(ctrl)
			got, err := FilterViewableIDs(publicCtx(), authService, "", nil, interfaces.AuthResourceTypeOperator)
			So(err, ShouldBeNil)
			So(got, ShouldBeEmpty)
		})

		Convey("类型级授权(ResourceIDAll)全量放行", func() {
			authService := mocks.NewMockIAuthorizationService(ctrl)
			authService.EXPECT().GetAccessor(gomock.Any(), "").Return(&interfaces.AuthAccessor{ID: "user-1"}, nil)
			authService.EXPECT().ResourceListIDs(gomock.Any(), gomock.Any(), interfaces.AuthResourceTypeOperator,
				interfaces.AuthOperationTypeView).Return([]string{interfaces.ResourceIDAll}, nil)
			got, err := FilterViewableIDs(publicCtx(), authService, "", ids, interfaces.AuthResourceTypeOperator)
			So(err, ShouldBeNil)
			So(got, ShouldResemble, ids)
		})

		Convey("只保留有查看权限的ID", func() {
			authService := mocks.NewMockIAuthorizationService(ctrl)
			authService.EXPECT().GetAccessor(gomock.Any(), "").Return(&interfaces.AuthAccessor{ID: "user-1"}, nil)
			authService.EXPECT().ResourceListIDs(gomock.Any(), gomock.Any(), interfaces.AuthResourceTypeSkill,
				interfaces.AuthOperationTypeView).Return([]string{"b", "z"}, nil)
			got, err := FilterViewableIDs(publicCtx(), authService, "", ids, interfaces.AuthResourceTypeSkill)
			So(err, ShouldBeNil)
			So(got, ShouldResemble, []string{"b"})
		})

		Convey("权限集为空则全部过滤掉", func() {
			authService := mocks.NewMockIAuthorizationService(ctrl)
			authService.EXPECT().GetAccessor(gomock.Any(), "").Return(&interfaces.AuthAccessor{ID: "user-1"}, nil)
			authService.EXPECT().ResourceListIDs(gomock.Any(), gomock.Any(), interfaces.AuthResourceTypeToolBox,
				interfaces.AuthOperationTypeView).Return([]string{}, nil)
			got, err := FilterViewableIDs(publicCtx(), authService, "", ids, interfaces.AuthResourceTypeToolBox)
			So(err, ShouldBeNil)
			So(got, ShouldBeEmpty)
		})

		Convey("授权服务报错向上传递，不降级放行", func() {
			authService := mocks.NewMockIAuthorizationService(ctrl)
			authService.EXPECT().GetAccessor(gomock.Any(), "").Return(&interfaces.AuthAccessor{ID: "user-1"}, nil)
			authService.EXPECT().ResourceListIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(nil, errors.New("authz down"))
			got, err := FilterViewableIDs(publicCtx(), authService, "", ids, interfaces.AuthResourceTypeOperator)
			So(err, ShouldNotBeNil)
			So(got, ShouldBeNil)
		})
	})
}
