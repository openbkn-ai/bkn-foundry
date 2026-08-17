// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package user_mgmt

import (
	"context"

	"vega-backend/common"
	"vega-backend/interfaces"
)

// NoopUserMgmtService Empty User Management Service (used when authentication is disabled)
type NoopUserMgmtService struct {
	appSetting *common.AppSetting
}

func NewNoopUserMgmtService(appSetting *common.AppSetting) interfaces.UserMgmtService {
	return &NoopUserMgmtService{appSetting: appSetting}
}

func (n *NoopUserMgmtService) GetAccountNames(ctx context.Context, accountInfos []*interfaces.AccountInfo) error {
	// When authentication is disabled, use "ID" as the name
	for _, info := range accountInfos {
		if info.Name == "" {
			info.Name = info.ID
		}
	}
	return nil
}
