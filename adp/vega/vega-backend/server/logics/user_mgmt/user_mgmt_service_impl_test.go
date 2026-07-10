package user_mgmt

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

func TestUserMgmtServiceImplGetAccountNames(t *testing.T) {
	t.Run("delegates to user management access", func(t *testing.T) {
		access := &fakeUserMgmtAccess{}
		service := &UserMgmtServiceImpl{uma: access}
		accounts := []*interfaces.AccountInfo{{ID: "u1", Type: interfaces.ACCESSOR_TYPE_USER}}

		err := service.GetAccountNames(context.Background(), accounts)

		require.NoError(t, err)
		assert.Equal(t, accounts, access.gotAccounts)
	})

	t.Run("returns user management access error", func(t *testing.T) {
		accounts := []*interfaces.AccountInfo{{ID: "u1", Type: interfaces.ACCESSOR_TYPE_USER}}
		service := &UserMgmtServiceImpl{uma: &fakeUserMgmtAccess{err: errors.New("lookup failed")}}

		err := service.GetAccountNames(context.Background(), accounts)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "lookup failed")
	})
}

type fakeUserMgmtAccess struct {
	gotAccounts []*interfaces.AccountInfo
	err         error
}

func (f *fakeUserMgmtAccess) GetAccountNames(_ context.Context, accountInfos []*interfaces.AccountInfo) error {
	f.gotAccounts = accountInfos
	return f.err
}
