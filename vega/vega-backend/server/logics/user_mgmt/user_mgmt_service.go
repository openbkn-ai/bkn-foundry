// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package user_mgmt

import (
	"sync"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

var (
	umServiceOnce sync.Once
	umService     interfaces.UserMgmtService
)

func NewUserMgmtService(appSetting *common.AppSetting) interfaces.UserMgmtService {
	umServiceOnce.Do(func() {
		umService = NewUserMgmtServiceImpl(appSetting)
	})
	return umService
}
