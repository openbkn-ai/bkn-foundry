package common

import (
	"context"
	"net/http"

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
)

// requireOperatorTypePermission 校验调用方在算子类型上持有指定操作权限。
//
// 用于 /function/execute、/ai_generate/* 这类不隶属于任何已存在资源的端点：它们操作的是
// 尚未落库的函数代码，没有资源 ID 可判，因此按类型级（ResourceIDAll）判定，口径与
// logics/auth/decision.go 中 CheckCreatePermission 一致。
//
// 仅在公开面生效。内部面（internal-v1）由服务间调用，身份来自 X-Account-ID 头而非经校验的
// 令牌，沿用服务内既有惯用法（见 logics/operator/query.go:31）跳过判定，避免打断现有调用方。
func requireOperatorTypePermission(
	ctx context.Context,
	authService interfaces.IAuthorizationService,
	operation interfaces.AuthOperationType,
) error {
	if !common.IsPublicAPIFromCtx(ctx) {
		return nil
	}
	authContext, ok := common.GetAccountAuthContextFromCtx(ctx)
	if !ok || authContext == nil {
		return errors.DefaultHTTPError(ctx, http.StatusUnauthorized, "authentication required")
	}
	accessor := &interfaces.AuthAccessor{
		ID:   authContext.AccountID,
		Type: authContext.AccountType,
	}
	authorized, err := authService.OperationCheckAll(ctx, accessor,
		interfaces.ResourceIDAll, interfaces.AuthResourceTypeOperator, operation)
	if err != nil {
		return err
	}
	if !authorized {
		return errors.NewHTTPError(ctx, http.StatusForbidden, forbiddenCodeFor(operation), nil)
	}
	return nil
}

// forbiddenCodeFor 返回与操作对应的拒绝错误码，使前端拿到的提示与动作一致。
func forbiddenCodeFor(operation interfaces.AuthOperationType) errors.ErrorCode {
	switch operation {
	case interfaces.AuthOperationTypeExecute:
		return errors.ErrExtCommonUseForbidden
	case interfaces.AuthOperationTypeCreate:
		return errors.ErrExtCommonAddForbidden
	default:
		return errors.ErrExtCommonViewForbidden
	}
}
