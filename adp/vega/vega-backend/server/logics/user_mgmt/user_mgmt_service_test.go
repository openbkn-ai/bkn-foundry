// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package user_mgmt

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/common"
	"vega-backend/interfaces"
)

func TestNoopUserMgmtServiceGetAccountNames(t *testing.T) {
	service := NewNoopUserMgmtService(&common.AppSetting{})
	accounts := []*interfaces.AccountInfo{
		{ID: "u1", Type: interfaces.ACCESSOR_TYPE_USER},
		{ID: "app1", Type: interfaces.ACCESSOR_TYPE_APP, Name: "Existing"},
	}

	err := service.GetAccountNames(context.Background(), accounts)

	require.NoError(t, err)
	assert.Equal(t, "u1", accounts[0].Name)
	assert.Equal(t, "Existing", accounts[1].Name)
}

func TestUserMgmtServiceImplDelegates(t *testing.T) {
	access := &fakeUserMgmtAccess{}
	service := &UserMgmtServiceImpl{uma: access}
	accounts := []*interfaces.AccountInfo{{ID: "u1", Type: interfaces.ACCESSOR_TYPE_USER}}

	require.NoError(t, service.GetAccountNames(context.Background(), accounts))
	assert.Equal(t, accounts, access.gotAccounts)

	access.err = errors.New("lookup failed")
	err := service.GetAccountNames(context.Background(), accounts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lookup failed")
}

type fakeUserMgmtAccess struct {
	gotAccounts []*interfaces.AccountInfo
	err         error
}

func (f *fakeUserMgmtAccess) GetAccountNames(_ context.Context, accountInfos []*interfaces.AccountInfo) error {
	f.gotAccounts = accountInfos
	return f.err
}
