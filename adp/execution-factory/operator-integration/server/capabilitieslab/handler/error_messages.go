// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package handler

import sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"

const (
	capabilitiesLabInvalidRequest  = "invalid_request"
	capabilitiesLabNotFound        = "not_found"
	capabilitiesLabFileRequired    = "file_required"
	capabilitiesLabFeatureDisabled = "feature_disabled"
)

var capabilitiesLabErrorMessages = map[string]map[string]string{
	sharedrest.SimplifiedChinese: {
		capabilitiesLabInvalidRequest:  "请求参数无效。",
		capabilitiesLabNotFound:        "未找到资源。",
		capabilitiesLabFileRequired:    "缺少必需的文件。",
		capabilitiesLabFeatureDisabled: "该功能未启用。",
	},
	sharedrest.AmericanEnglish: {
		capabilitiesLabInvalidRequest:  "Invalid request parameters.",
		capabilitiesLabNotFound:        "Resource not found.",
		capabilitiesLabFileRequired:    "A required file is missing.",
		capabilitiesLabFeatureDisabled: "This feature is disabled.",
	},
}

func capabilitiesLabErrorMessage(language, code string) string {
	if messages, ok := capabilitiesLabErrorMessages[language]; ok {
		if message, ok := messages[code]; ok {
			return message
		}
	}
	return capabilitiesLabErrorMessages[sharedrest.SimplifiedChinese][code]
}
