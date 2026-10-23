// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package locale

import (
	"sync"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
)

var registerOnce sync.Once

func Register() {
	registerOnce.Do(func() {
		i18n.RegisterI18n(&auditTextEnUS)
		i18n.RegisterI18n(&auditTextZhCN)
		i18n.RegisterI18n(&buildTaskEnUS)
		i18n.RegisterI18n(&buildTaskZhCN)
		i18n.RegisterI18n(&catalogEnUS)
		i18n.RegisterI18n(&catalogHealthCheckScheduleEnUS)
		i18n.RegisterI18n(&catalogHealthCheckScheduleZhCN)
		i18n.RegisterI18n(&catalogZhCN)
		i18n.RegisterI18n(&connectorTypeEnUS)
		i18n.RegisterI18n(&connectorTypeZhCN)
		i18n.RegisterI18n(&datasetEnUS)
		i18n.RegisterI18n(&datasetZhCN)
		i18n.RegisterI18n(&discoverScheduleEnUS)
		i18n.RegisterI18n(&discoverScheduleZhCN)
		i18n.RegisterI18n(&discoverTaskEnUS)
		i18n.RegisterI18n(&discoverTaskZhCN)
		i18n.RegisterI18n(&errorcodeEnUS)
		i18n.RegisterI18n(&errorcodeZhCN)
		i18n.RegisterI18n(&indexCapabilityEnUS)
		i18n.RegisterI18n(&indexCapabilityZhCN)
		i18n.RegisterI18n(&logicalViewEnUS)
		i18n.RegisterI18n(&logicalViewZhCN)
		i18n.RegisterI18n(&resourceEnUS)
		i18n.RegisterI18n(&resourceZhCN)
		i18n.RegisterI18n(&semanticUnderstandingTaskEnUS)
		i18n.RegisterI18n(&semanticUnderstandingTaskZhCN)

		if err := i18n.ValidateMessageBundles(); err != nil {
			panic(err)
		}
	})
}
