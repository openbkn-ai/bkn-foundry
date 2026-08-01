package toolbox

import (
	"context"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

// TestGetToolBoxNamesByIDsAuthz 覆盖 #345：批量取名按查看权限过滤，避免枚举全量工具箱名
func TestGetToolBoxNamesByIDsAuthz(t *testing.T) {
	Convey("工具箱批量取名授权过滤", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		publicCtx := common.SetPublicAPIToCtx(context.Background(), true)

		Convey("只返回有查看权限的工具箱", func() {
			authService := mocks.NewMockIAuthorizationService(ctrl)
			toolBoxDB := mocks.NewMockIToolboxDB(ctrl)
			svc := &ToolServiceImpl{
				Logger:      logger.DefaultLogger(),
				ToolBoxDB:   toolBoxDB,
				AuthService: authService,
			}
			authService.EXPECT().GetAccessor(gomock.Any(), "").Return(&interfaces.AuthAccessor{ID: "user-1"}, nil)
			authService.EXPECT().ResourceListIDs(gomock.Any(), gomock.Any(), interfaces.AuthResourceTypeToolBox,
				interfaces.AuthOperationTypeView).Return([]string{"box-1"}, nil)
			toolBoxDB.EXPECT().SelectListByBoxIDs(gomock.Any(), []string{"box-1"}).
				Return([]*model.ToolboxDB{{BoxID: "box-1", Name: "工具箱一"}}, nil)

			resp, err := svc.GetToolBoxNamesByIDs(publicCtx, []string{"box-1", "box-2"})
			So(err, ShouldBeNil)
			So(len(resp.Entries), ShouldEqual, 1)
			So(resp.Entries[0].ID, ShouldEqual, "box-1")
		})

		Convey("权限集为空时返回空列表且不查库", func() {
			authService := mocks.NewMockIAuthorizationService(ctrl)
			svc := &ToolServiceImpl{
				Logger:      logger.DefaultLogger(),
				ToolBoxDB:   mocks.NewMockIToolboxDB(ctrl),
				AuthService: authService,
			}
			authService.EXPECT().GetAccessor(gomock.Any(), "").Return(&interfaces.AuthAccessor{ID: "user-1"}, nil)
			authService.EXPECT().ResourceListIDs(gomock.Any(), gomock.Any(), interfaces.AuthResourceTypeToolBox,
				interfaces.AuthOperationTypeView).Return(nil, nil)

			resp, err := svc.GetToolBoxNamesByIDs(publicCtx, []string{"box-1"})
			So(err, ShouldBeNil)
			So(resp.Entries, ShouldBeEmpty)
		})

		Convey("内部面不过滤", func() {
			// authService 不设 EXPECT：内部面若发起判定，gomock 会因非预期调用失败
			toolBoxDB := mocks.NewMockIToolboxDB(ctrl)
			svc := &ToolServiceImpl{
				Logger:      logger.DefaultLogger(),
				ToolBoxDB:   toolBoxDB,
				AuthService: mocks.NewMockIAuthorizationService(ctrl),
			}
			toolBoxDB.EXPECT().SelectListByBoxIDs(gomock.Any(), []string{"box-1"}).
				Return([]*model.ToolboxDB{{BoxID: "box-1", Name: "工具箱一"}}, nil)

			resp, err := svc.GetToolBoxNamesByIDs(context.Background(), []string{"box-1"})
			So(err, ShouldBeNil)
			So(len(resp.Entries), ShouldEqual, 1)
		})
	})
}
