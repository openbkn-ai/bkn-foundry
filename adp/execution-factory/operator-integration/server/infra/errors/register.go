// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package errors

import (
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/locale"
)

func init() {
	i18n.RegisterI18n(&locale.TextMessagesEnUS)
	i18n.RegisterI18n(&locale.TextMessagesZhCN)
	i18n.RegisterI18n(&locale.ErrorMessagesEnUS)
	i18n.RegisterI18n(&locale.ErrorMessagesZhCN)
	if err := i18n.ValidateMessageBundles(); err != nil {
		panic(err)
	}
	ids := make([]string, 0, len(locale.ErrorMessagesEnUS.Messages))
	for id := range locale.ErrorMessagesEnUS.Messages {
		if !strings.Contains(id, ".Detail.") {
			ids = append(ids, id)
		}
	}
	httperrors.Register(ids)
}
