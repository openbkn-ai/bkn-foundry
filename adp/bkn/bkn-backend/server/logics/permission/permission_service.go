// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package permission

import (
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

var (
	pServiceOnce sync.Once
	pService     interfaces.PermissionService
)

func NewPermissionService(appSetting *common.AppSetting) interfaces.PermissionService {
	pServiceOnce.Do(func() {
		pService = NewPermissionServiceImpl(appSetting)
	})
	return pService
}
