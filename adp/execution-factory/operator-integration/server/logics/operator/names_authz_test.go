package operator

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

// TestGetOperatorNamesByIDsAuthz covers #345: Batch name selection is filtered by viewing permissions to avoid enumerating all operator names.
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
			// authService does not set EXPECT: if a judgment is initiated internally, gomock will fail due to unexpected calls.
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

func TestProjectOperatorAuthorizeOperations(t *testing.T) {
	Convey("Operator list projects authorize with the existing accessor", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		authService := mocks.NewMockIAuthorizationService(ctrl)
		accessor := &interfaces.AuthAccessor{ID: "user-1"}
		operators := []*interfaces.OperatorDataInfo{{OperatorID: "op-1"}, {OperatorID: "op-2"}}
		authService.EXPECT().ResourceFilterOperations(
			gomock.Any(), accessor, []string{"op-1", "op-2"}, interfaces.AuthResourceTypeOperator,
			[]interfaces.AuthOperationType{interfaces.AuthOperationTypeView},
			[]interfaces.AuthOperationType{interfaces.AuthOperationTypeAuthorize},
		).Return(map[string][]interfaces.AuthOperationType{
			"op-2": {interfaces.AuthOperationTypeAuthorize},
		}, nil)

		err := projectOperatorAuthorizeOperations(common.SetPublicAPIToCtx(context.Background(), true), authService, accessor, operators)

		So(err, ShouldBeNil)
		So(operators[0].Operations, ShouldBeEmpty)
		So(operators[1].Operations, ShouldResemble, []interfaces.AuthOperationType{interfaces.AuthOperationTypeAuthorize})
	})
}
