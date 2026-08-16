// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package user_mgmt

import (
	"context"

	"bkn-backend/common"
	"bkn-backend/interfaces"
)

// NoopUserMgmtService is used when authentication is disabled.
type NoopUserMgmtService struct {
	appSetting *common.AppSetting
}

func NewNoopUserMgmtService(appSetting *common.AppSetting) interfaces.UserMgmtService {
	return &NoopUserMgmtService{appSetting: appSetting}
}

func (n *NoopUserMgmtService) GetAccountNames(ctx context.Context, accountInfos []*interfaces.AccountInfo) error {
	// Use the ID as the name when authentication is disabled.
	for _, info := range accountInfos {
		if info.Name == "" {
			info.Name = info.ID
		}
	}
	return nil
}
