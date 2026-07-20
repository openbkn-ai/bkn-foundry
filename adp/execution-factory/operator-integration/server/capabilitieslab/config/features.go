// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package config

import "os"

type FeatureFlags struct {
	Catalog        bool `json:"catalog"`
	Function       bool `json:"function"`
	Impex          bool `json:"impex"`
	McpSseWizard   bool `json:"mcp_sse_wizard"`
	SkillFiles     bool `json:"skill_files"`
	HideLegacyMenu bool `json:"hide_legacy_execution_factory_menu"`
}

func LoadFeatureFlags() FeatureFlags {
	return FeatureFlags{
		Catalog:        envBool("LAB_FEATURE_CATALOG", true),
		Function:       envBool("LAB_FEATURE_FUNCTION", true),
		Impex:          envBool("LAB_FEATURE_IMPEX", true),
		McpSseWizard:   envBool("LAB_FEATURE_MCP_SSE_WIZARD", true),
		SkillFiles:     envBool("LAB_FEATURE_SKILL_FILES", true),
		HideLegacyMenu: envBool("LAB_HIDE_LEGACY_EXECUTION_FACTORY", false),
	}
}

func envBool(key string, defaultValue bool) bool {
	raw := os.Getenv(key)
	if raw == "" {
		return defaultValue
	}

	return raw != "false" && raw != "0"
}
