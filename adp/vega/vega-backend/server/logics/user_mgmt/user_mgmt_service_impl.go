// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package user_mgmt

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/logics"
)

type UserMgmtServiceImpl struct {
	appSetting *common.AppSetting
	uma        interfaces.UserMgmtAccess
}

func NewUserMgmtServiceImpl(appSetting *common.AppSetting) interfaces.UserMgmtService {
	return &UserMgmtServiceImpl{
		appSetting: appSetting,
		uma:        logics.UMA,
	}
}

func (s *UserMgmtServiceImpl) GetAccountNames(ctx context.Context, accountInfos []*interfaces.AccountInfo) error {
	return s.uma.GetAccountNames(ctx, accountInfos)
}
