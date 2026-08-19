package auth

import (
	"context"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	oerrors "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

// GetAccessor gets visitor information.
func (s *authServiceImpl) GetAccessor(ctx context.Context, userID string) (*interfaces.AuthAccessor, error) {
	// Read account authentication context from context.
	authContext, ok := common.GetAccountAuthContextFromCtx(ctx)
	if !ok {
		authContext = &interfaces.AccountAuthContext{
			AccountID:   userID,
			AccountType: interfaces.AccessorTypeUser, // Default user type.
		}
	}

	// Internal interfaces are only accessible to real-name users.
	if authContext.AccountID == "" {
		return nil, oerrors.NewHTTPError(ctx, http.StatusNotFound, oerrors.ErrExtCommonUserNotFound, "userID is empty")
	}
	accessor := &interfaces.AuthAccessor{
		ID: authContext.AccountID,
	}
	switch authContext.AccountType {
	case interfaces.AccessorTypeUser, interfaces.AccessorTypeAnonymous:
		// real-name user, anonymous user.
		userInfos, err := s.userManagement.GetUsersInfo(ctx, []string{authContext.AccountID}, []string{interfaces.DisplayName})
		if err != nil {
			return nil, err
		}
		if len(userInfos) == 0 {
			return nil, oerrors.NewHTTPError(ctx, http.StatusNotFound, oerrors.ErrExtCommonUserNotFound, nil)
		}
		accessor.Type = interfaces.AccessorTypeUser
		accessor.Name = userInfos[0].DisplayName
	case interfaces.AccessorTypeApp:
		// application account.
		appInfo, err := s.userManagement.GetAppInfo(ctx, authContext.AccountID)
		if err != nil {
			return nil, err
		}
		accessor.Type = interfaces.AccessorTypeApp
		accessor.Name = appInfo.Name
	case interfaces.AccessorTypeDepartment, interfaces.AccessorTypeGroup, interfaces.AccessorTypeRole:
		return nil, oerrors.NewHTTPError(ctx, http.StatusForbidden, oerrors.ErrExtCommonDepartmentOrGroupOrRoleNotAllowed,
			"department, group or role account not allowed")
	default:
		return nil, oerrors.NewHTTPError(ctx, http.StatusForbidden, oerrors.ErrExtCommonInvalidAccessorType, "invalid accessor type")
	}
	return accessor, nil
}
