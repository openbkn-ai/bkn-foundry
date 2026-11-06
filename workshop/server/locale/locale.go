// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package locale holds the messages the community skeleton renders itself.
// Business messages are registered by the build that assembles the business
// routes.
package locale

import (
	"sync"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
)

var registerOnce sync.Once

// Register installs the service messages once.
func Register() {
	registerOnce.Do(func() {
		i18n.RegisterI18n(&workshopEnUS)
		i18n.RegisterI18n(&workshopZhCN)

		if err := i18n.ValidateMessageBundles(); err != nil {
			panic(err)
		}
	})
}

var workshopZhCN = i18n.I18nMessages{
	Locale: i18n.SimplifiedChinese,
	Messages: map[string]string{
		"Workshop.Validation.Detail.AuthenticationFailed":  "认证失败。",
		"Workshop.Validation.Detail.DependencyUnavailable": "数据库不可达。",
	},
}

var workshopEnUS = i18n.I18nMessages{
	Locale: i18n.AmericanEnglish,
	Messages: map[string]string{
		"Workshop.Validation.Detail.AuthenticationFailed":  "Authentication failed.",
		"Workshop.Validation.Detail.DependencyUnavailable": "The database is unreachable.",
	},
}
