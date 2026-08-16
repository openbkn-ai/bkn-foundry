// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package handler

import "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/localize"

const (
	capabilitiesLabInvalidRequest  = "invalid_request"
	capabilitiesLabNotFound        = "not_found"
	capabilitiesLabFileRequired    = "file_required"
	capabilitiesLabFeatureDisabled = "feature_disabled"
)

func capabilitiesLabErrorMessage(language, code string) string {
	return localize.NewI18nTranslator(language).Trans("capabilities_lab." + code)
}
