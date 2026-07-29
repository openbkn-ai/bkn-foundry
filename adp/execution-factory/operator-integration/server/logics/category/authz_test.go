package category

import (
	"context"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

// TestCategoryWriteAuthz 覆盖 #345：公开面分类写操作的类型级门禁。
// 各用例都不给 DBCategory/Validator 设 EXPECT——门禁一旦失效走到业务逻辑，
// gomock 会因非预期调用直接失败。
func TestCategoryWriteAuthz(t *testing.T) {
	Convey("算子分类写操作授权", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		publicCtx := common.SetPublicAPIToCtx(context.Background(), true)

		newManager := func(authService interfaces.IAuthorizationService) *categoryManager {
			return &categoryManager{
				logger:      logger.DefaultLogger(),
				DBTx:        mocks.NewMockDBTx(ctrl),
				DBCategory:  mocks.NewMockDBCategory(ctrl),
				Validator:   mocks.NewMockValidator(ctrl),
				Cache:       mocks.NewMockCache(ctrl),
				AuthService: authService,
			}
		}
		expectDenied := func(authService *mocks.MockIAuthorizationService, operation interfaces.AuthOperationType) {
			authService.EXPECT().GetAccessor(gomock.Any(), "user-1").Return(&interfaces.AuthAccessor{ID: "user-1"}, nil)
			authService.EXPECT().OperationCheckAll(gomock.Any(), gomock.Any(), interfaces.ResourceIDAll,
				interfaces.AuthResourceTypeOperator, operation).Return(false, nil)
		}

		Convey("CreateCategory 无新建权限时拒绝", func() {
			authService := mocks.NewMockIAuthorizationService(ctrl)
			expectDenied(authService, interfaces.AuthOperationTypeCreate)
			resp, err := newManager(authService).CreateCategory(publicCtx, &interfaces.CreateCategoryReq{
				UserID: "user-1", CategoryType: "cat-1", CategoryName: "分类一",
			})
			So(resp, ShouldBeNil)
			So(err, ShouldNotBeNil)
		})

		Convey("UpdateCategory 无编辑权限时拒绝", func() {
			authService := mocks.NewMockIAuthorizationService(ctrl)
			expectDenied(authService, interfaces.AuthOperationTypeModify)
			resp, err := newManager(authService).UpdateCategory(publicCtx, &interfaces.UpdateCategoryReq{
				UserID: "user-1", CategoryType: "cat-1", CategoryName: "分类一",
			})
			So(resp, ShouldBeNil)
			So(err, ShouldNotBeNil)
		})

		Convey("DeleteCategory 无删除权限时拒绝", func() {
			authService := mocks.NewMockIAuthorizationService(ctrl)
			expectDenied(authService, interfaces.AuthOperationTypeDelete)
			err := newManager(authService).DeleteCategory(publicCtx, &interfaces.DeleteCategoryReq{
				UserID: "user-1", CategoryType: "cat-1",
			})
			So(err, ShouldNotBeNil)
		})

		Convey("内部面不判定（启动期内置分类灌入走此路）", func() {
			// authService 不设 EXPECT：内部面若发起判定，gomock 会因非预期调用失败
			manager := newManager(mocks.NewMockIAuthorizationService(ctrl))
			manager.DBCategory.(*mocks.MockDBCategory).EXPECT().
				SelectListByCategoryID(gomock.Any(), gomock.Nil(), "cat-1").Return(nil, nil)
			err := manager.DeleteCategory(context.Background(), &interfaces.DeleteCategoryReq{
				UserID: "user-1", CategoryType: "cat-1",
			})
			// 分类不存在 → 404，说明已越过门禁进入业务逻辑
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "not found")
		})

		Convey("读接口保持开放，不做判定", func() {
			manager := newManager(mocks.NewMockIAuthorizationService(ctrl))
			manager.DBCategory.(*mocks.MockDBCategory).EXPECT().
				SelectList(gomock.Any(), gomock.Nil()).Return(nil, nil)
			list, err := manager.GetCategoryList(publicCtx)
			So(err, ShouldBeNil)
			So(len(list), ShouldEqual, 2) // 两个内置分类：未分类、系统工具
		})
	})
}
