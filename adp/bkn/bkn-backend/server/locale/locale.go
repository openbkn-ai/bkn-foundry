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

// Register installs the service messages once before error code registration.
func Register() {
	registerOnce.Do(func() {
		i18n.RegisterI18n(&auditTextEnUS)
		i18n.RegisterI18n(&auditTextZhCN)
		i18n.RegisterI18n(&valueTypeEnUS)
		i18n.RegisterI18n(&valueTypeZhCN)
		i18n.RegisterI18n(&actionScheduleEnUS)
		i18n.RegisterI18n(&actionScheduleZhCN)
		i18n.RegisterI18n(&actionTypeEnUS)
		i18n.RegisterI18n(&actionTypeZhCN)
		i18n.RegisterI18n(&capabilityBindingEnUS)
		i18n.RegisterI18n(&capabilityBindingZhCN)
		i18n.RegisterI18n(&conceptGroupEnUS)
		i18n.RegisterI18n(&conceptGroupZhCN)
		i18n.RegisterI18n(&cypherEnUS)
		i18n.RegisterI18n(&cypherZhCN)
		i18n.RegisterI18n(&errorcodeEnUS)
		i18n.RegisterI18n(&errorcodeZhCN)
		i18n.RegisterI18n(&knDiffEnUS)
		i18n.RegisterI18n(&knDiffZhCN)
		i18n.RegisterI18n(&knowledgeNetworkEnUS)
		i18n.RegisterI18n(&knowledgeNetworkZhCN)
		i18n.RegisterI18n(&objectMetricEnUS)
		i18n.RegisterI18n(&objectMetricZhCN)
		i18n.RegisterI18n(&objectTypeEnUS)
		i18n.RegisterI18n(&objectTypeZhCN)
		i18n.RegisterI18n(&relationTypeEnUS)
		i18n.RegisterI18n(&relationTypeZhCN)
		i18n.RegisterI18n(&riskTypeEnUS)
		i18n.RegisterI18n(&riskTypeZhCN)

		if err := i18n.ValidateMessageBundles(); err != nil {
			panic(err)
		}
	})
}
