package common

import (
	"context"
	"testing"

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/mocks"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

const testAccountID = "11111111-1111-4111-8111-111111111111"

// publicCtx 构造一个带已校验身份的公开面 context，等价于 middlewareIntrospectVerify 的产物。
func publicCtx() context.Context {
	ctx := common.SetPublicAPIToCtx(context.Background(), true)
	return common.SetAccountAuthContextToCtx(ctx, &interfaces.AccountAuthContext{
		AccountID:   testAccountID,
		AccountType: interfaces.AccessorTypeUser,
	})
}

func TestRequireOperatorTypePermission(t *testing.T) {
	Convey("公开面持有权限时放行", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		authService := mocks.NewMockIAuthorizationService(ctrl)
		authService.EXPECT().
			OperationCheckAll(gomock.Any(), gomock.Any(), interfaces.ResourceIDAll,
				interfaces.AuthResourceTypeOperator, interfaces.AuthOperationTypeExecute).
			Return(true, nil)

		err := requireOperatorTypePermission(publicCtx(), authService, interfaces.AuthOperationTypeExecute)

		So(err, ShouldBeNil)
	})

	Convey("公开面缺权限时拒绝，错误码随操作而变", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		authService := mocks.NewMockIAuthorizationService(ctrl)
		authService.EXPECT().
			OperationCheckAll(gomock.Any(), gomock.Any(), interfaces.ResourceIDAll,
				interfaces.AuthResourceTypeOperator, interfaces.AuthOperationTypeExecute).
			Return(false, nil)

		err := requireOperatorTypePermission(publicCtx(), authService, interfaces.AuthOperationTypeExecute)

		So(err, ShouldNotBeNil)
	})

	Convey("公开面无身份时按未认证拒绝，且不去问授权服务", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		authService := mocks.NewMockIAuthorizationService(ctrl)

		ctx := common.SetPublicAPIToCtx(context.Background(), true)
		err := requireOperatorTypePermission(ctx, authService, interfaces.AuthOperationTypeExecute)

		So(err, ShouldNotBeNil)
	})

	Convey("内部面直接放行，不触发授权判定", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		authService := mocks.NewMockIAuthorizationService(ctrl)

		err := requireOperatorTypePermission(context.Background(), authService, interfaces.AuthOperationTypeExecute)

		So(err, ShouldBeNil)
	})
}

func TestForbiddenCodeFor(t *testing.T) {
	Convey("拒绝错误码与动作对应", t, func() {
		So(forbiddenCodeFor(interfaces.AuthOperationTypeExecute), ShouldEqual, errors.ErrExtCommonUseForbidden)
		So(forbiddenCodeFor(interfaces.AuthOperationTypeCreate), ShouldEqual, errors.ErrExtCommonAddForbidden)
		So(forbiddenCodeFor(interfaces.AuthOperationTypeView), ShouldEqual, errors.ErrExtCommonViewForbidden)
	})
}
