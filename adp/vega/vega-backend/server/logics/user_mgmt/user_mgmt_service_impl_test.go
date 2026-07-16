package user_mgmt

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

func TestUserMgmtServiceImplGetAccountNames(t *testing.T) {
	t.Run("delegates to user management access", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		access := vmock.NewMockUserMgmtAccess(ctrl)
		service := &UserMgmtServiceImpl{uma: access}
		accounts := []*interfaces.AccountInfo{{ID: "u1", Type: interfaces.ACCESSOR_TYPE_USER}}
		access.EXPECT().GetAccountNames(gomock.Any(), accounts).Return(nil)

		err := service.GetAccountNames(context.Background(), accounts)

		require.NoError(t, err)
	})

	t.Run("returns user management access error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		accounts := []*interfaces.AccountInfo{{ID: "u1", Type: interfaces.ACCESSOR_TYPE_USER}}
		access := vmock.NewMockUserMgmtAccess(ctrl)
		access.EXPECT().GetAccountNames(gomock.Any(), accounts).Return(errors.New("lookup failed"))
		service := &UserMgmtServiceImpl{uma: access}

		err := service.GetAccountNames(context.Background(), accounts)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "lookup failed")
	})
}
