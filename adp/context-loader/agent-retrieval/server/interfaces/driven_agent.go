// Copyright 2026 openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

// metric/operator 动态参数生成的入参类型。原由 agent-factory agent 消费，
// 现由直连 LLM（knlogicpropertyresolver.dynamicParamsLLM）使用，序列化后作为 LLM user 消息。

// MetricDynamicParamsGeneratorReq Metric Dynamic Params Generator Request
type MetricDynamicParamsGeneratorReq struct {
	LogicProperty     *LogicPropertyDef `json:"logic_property"`
	Query             string            `json:"query"`
	UniqueIdentities  []map[string]any  `json:"unique_identities"`
	AdditionalContext string            `json:"additional_context,omitempty"`
	NowMs             int64             `json:"now_ms,omitempty"`
	Timezone          string            `json:"timezone,omitempty"`
}

// OperatorDynamicParamsGeneratorReq Operator Dynamic Params Generator Request
type OperatorDynamicParamsGeneratorReq struct {
	OperatorID        string            `json:"operator_id"`
	LogicProperty     *LogicPropertyDef `json:"logic_property"`
	Query             string            `json:"query"`
	UniqueIdentities  []map[string]any  `json:"unique_identities"`
	AdditionalContext string            `json:"additional_context,omitempty"`
	// ObjectInstances removed, object instance information is passed via AdditionalContext
}
