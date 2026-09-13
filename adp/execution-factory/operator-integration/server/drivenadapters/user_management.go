// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package drivenadapters defines outbound service adapters.
package drivenadapters

import (
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

var (
	syncOnce sync.Once
	um       interfaces.UserManagement
)

// NewUserManagementClient creates the bkn-safe directory adapter.
func NewUserManagementClient() interfaces.UserManagement {
	syncOnce.Do(func() {
		conf := config.NewConfigLoader()
		baseURL := mustBknSafeURL()
		conf.GetLogger().Infof("[user-mgnt] provider=bkn-safe directory at %s", baseURL)
		um = newSafeUserManagement(baseURL, conf.GetLogger())
	})
	return um
}
