// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import (
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
)

// Audit ledger snapshots retain their stable business language independently of request language.
func AuditSummary(action, target, name string) string {
	actionText, actionErr := i18n.Render(i18n.SimplifiedChinese, "BknBackend.Audit.Action."+action, nil)
	if actionErr != nil {
		logger.Errorf("audit action rendering failed: %v", actionErr)
	}
	targetText, targetErr := i18n.Render(i18n.SimplifiedChinese, "BknBackend.Audit.Target."+target, nil)
	if targetErr != nil {
		logger.Errorf("audit target rendering failed: %v", targetErr)
	}
	text, err := i18n.Render(i18n.SimplifiedChinese, "BknBackend.Audit.Summary", map[string]any{"Action": actionText, "Target": targetText, "Name": name})
	if err != nil {
		logger.Errorf("audit summary rendering failed: %v", err)
	}
	return text
}
func AuditNames(names []string) string {
	if len(names) == 1 {
		return names[0]
	}
	separator, err := i18n.Render(i18n.SimplifiedChinese, "BknBackend.Audit.NameSeparator", nil)
	if err != nil {
		logger.Errorf("audit separator rendering failed: %v", err)
	}
	shown := make([]string, 0, 5)
	for _, name := range names {
		if len(shown) == 5 || len(strings.Join(shown, separator))+len(name) > 400 {
			break
		}
		shown = append(shown, name)
	}
	if len(shown) == 0 {
		return ""
	}
	key := "BknBackend.Audit.Names"
	if len(shown) < len(names) {
		key = "BknBackend.Audit.NamesTruncated"
	}
	text, err := i18n.Render(i18n.SimplifiedChinese, key, map[string]any{"Names": strings.Join(shown, separator), "Count": len(names)})
	if err != nil {
		logger.Errorf("audit names rendering failed: %v", err)
	}
	return text
}
