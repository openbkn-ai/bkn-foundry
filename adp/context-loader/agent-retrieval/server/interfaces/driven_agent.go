// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

// ToolDynamicParamsGeneratorReq describes a ToolBox logical-property request.
type ToolDynamicParamsGeneratorReq struct {
	BoxID             string            `json:"box_id"`
	ToolID            string            `json:"tool_id"`
	LogicProperty     *LogicPropertyDef `json:"logic_property"`
	Query             string            `json:"query"`
	UniqueIdentities  []map[string]any  `json:"unique_identities"`
	AdditionalContext string            `json:"additional_context,omitempty"`
	// ObjectInstances removed, object instance information is passed via AdditionalContext
}
