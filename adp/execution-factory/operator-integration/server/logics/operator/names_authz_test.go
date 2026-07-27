package operator

import (
	"context"
	"testing"

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/mocks"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

// TestGetOperatorNamesByIDsAuthz 覆盖 #345：批量取名按查看权限过滤，避免枚举全量算子名
func TestGetOperatorNamesByIDsAuthz(t *testing.T) {
	Convey("算子批量取名授权过滤", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		publicCtx := common.SetPublicAPIToCtx(context.Background(), true)

		Convey("只返回有查看权限的算子", func() {
			authService := mocks.NewMockIAuthorizationService(ctrl)
			operatorDB := mocks.NewMockIOperatorRegisterDB(ctrl)
			manager := &operatorManager{
				Logger:            logger.DefaultLogger(),
				DBOperatorManager: operatorDB,
				AuthService:       authService,
			}
			authService.EXPECT().GetAccessor(gomock.Any(), "").Return(&interfaces.AuthAccessor{ID: "user-1"}, nil)
			authService.EXPECT().ResourceListIDs(gomock.Any(), gomock.Any(), interfaces.AuthResourceTypeOperator,
				interfaces.AuthOperationTypeView).Return([]string{"op-1"}, nil)
			operatorDB.EXPECT().SelectByOperatorIDs(gomock.Any(), []string{"op-1"}).
				Return([]*model.OperatorRegisterDB{{OperatorID: "op-1", Name: "算子一"}}, nil)

			resp, err := manager.GetOperatorNamesByIDs(publicCtx, []string{"op-1", "op-2"})
			So(err, ShouldBeNil)
			So(len(resp.Entries), ShouldEqual, 1)
			So(resp.Entries[0].ID, ShouldEqual, "op-1")
		})

		Convey("权限集为空时返回空列表且不查库", func() {
			authService := mocks.NewMockIAuthorizationService(ctrl)
			manager := &operatorManager{
				Logger:            logger.DefaultLogger(),
				DBOperatorManager: mocks.NewMockIOperatorRegisterDB(ctrl),
				AuthService:       authService,
			}
			authService.EXPECT().GetAccessor(gomock.Any(), "").Return(&interfaces.AuthAccessor{ID: "user-1"}, nil)
			authService.EXPECT().ResourceListIDs(gomock.Any(), gomock.Any(), interfaces.AuthResourceTypeOperator,
				interfaces.AuthOperationTypeView).Return(nil, nil)

			resp, err := manager.GetOperatorNamesByIDs(publicCtx, []string{"op-1"})
			So(err, ShouldBeNil)
			So(resp.Entries, ShouldBeEmpty)
		})

		Convey("内部面不过滤", func() {
			// authService 不设 EXPECT：内部面若发起判定，gomock 会因非预期调用失败
			operatorDB := mocks.NewMockIOperatorRegisterDB(ctrl)
			manager := &operatorManager{
				Logger:            logger.DefaultLogger(),
				DBOperatorManager: operatorDB,
				AuthService:       mocks.NewMockIAuthorizationService(ctrl),
			}
			operatorDB.EXPECT().SelectByOperatorIDs(gomock.Any(), []string{"op-1"}).
				Return([]*model.OperatorRegisterDB{{OperatorID: "op-1", Name: "算子一"}}, nil)

			resp, err := manager.GetOperatorNamesByIDs(context.Background(), []string{"op-1"})
			So(err, ShouldBeNil)
			So(len(resp.Entries), ShouldEqual, 1)
		})
	})
}
