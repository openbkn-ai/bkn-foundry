// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package auth

import (
	"sync"

	"ontology-query/common"
	"ontology-query/interfaces"
)

var (
	authServiceOnce sync.Once
	authService     interfaces.AuthService
)

func NewAuthService(appSetting *common.AppSetting) interfaces.AuthService {
	authServiceOnce.Do(func() {
		authService = NewHydraAuthService(appSetting)
	})
	return authService
}
