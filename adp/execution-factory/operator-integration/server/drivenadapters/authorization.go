// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

var (
	authOnce sync.Once
	auth     interfaces.Authorization
)

// NewAuthorization creates the bkn-safe authorization adapter.
func NewAuthorization() interfaces.Authorization {
	authOnce.Do(func() {
		conf := config.NewConfigLoader()
		baseURL := mustBknSafeURL()
		conf.GetLogger().Infof("[authz] provider=bkn-safe at %s", baseURL)
		auth = newSafeAuthorization(baseURL, conf.GetLogger())
	})
	return auth
}
