// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package knlogicpropertyresolver provides logic property resolver service for knowledge network.
package knlogicpropertyresolver

import (
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// DebugCollector Debug information collector.
type DebugCollector struct {
	propertyTypes  map[string]string
	agentRequests  map[string]interfaces.AgentRequestDebugInfo
	agentResponses map[string]*interfaces.AgentResponseDebugInfo
	dynamicParams  map[string]any
	nowMs          int64
	traceID        string
	warnings       []string
}

// NewDebugCollector creates a Debug information collector.
func NewDebugCollector() *DebugCollector {
	return &DebugCollector{
		propertyTypes:  make(map[string]string),
		agentRequests:  make(map[string]interfaces.AgentRequestDebugInfo),
		agentResponses: make(map[string]*interfaces.AgentResponseDebugInfo),
		dynamicParams:  make(map[string]any),
		warnings:       make([]string, 0),
	}
}

// AddPropertyType Add property type.
func (dc *DebugCollector) AddPropertyType(propertyName, propertyType string) {
	dc.propertyTypes[propertyName] = propertyType
}

// RecordMetricAgentRequest records the Metric Agent request (directly stores the Agent request structure)
func (dc *DebugCollector) RecordMetricAgentRequest(
	propertyName string,
	agentReq *interfaces.MetricDynamicParamsGeneratorReq,
) {
	// Store the Agent request structure directly.
	dc.agentRequests[propertyName] = agentReq
}

// RecordToolAgentRequest stores a ToolBox-tool parameter generation request.
func (dc *DebugCollector) RecordToolAgentRequest(
	propertyName string,
	agentReq *interfaces.ToolDynamicParamsGeneratorReq,
) {
	// Store the Agent request structure directly.
	dc.agentRequests[propertyName] = agentReq
}

// RecordAgentResponseSuccess records Agent's successful response (directly stores Agent response)
func (dc *DebugCollector) RecordAgentResponseSuccess(propertyName string, dynamicParams map[string]any) {
	// Store Agent response directly: return dynamicParams on success.
	dc.agentResponses[propertyName] = &interfaces.AgentResponseDebugInfo{
		DynamicParams: dynamicParams,
	}

	// Also collected dynamic_params.
	dc.dynamicParams[propertyName] = dynamicParams
}

// RecordAgentResponseMissingParams records Agent’s missing parameter response (directly stores Agent response)
func (dc *DebugCollector) RecordAgentResponseMissingParams(
	propertyName string,
	missingParams *interfaces.MissingPropertyParams,
) {
	errorMsg := ""
	if missingParams != nil {
		errorMsg = missingParams.ErrorMsg
	}

	// Store the Agent response directly; return the _error field on failure.
	dc.agentResponses[propertyName] = &interfaces.AgentResponseDebugInfo{
		Error: errorMsg,
	}
}

// RecordAgentResponseError records Agent error response (directly stores Agent response)
func (dc *DebugCollector) RecordAgentResponseError(propertyName, errorMsg string) {
	// Store the Agent response directly; return the _error field on failure.
	dc.agentResponses[propertyName] = &interfaces.AgentResponseDebugInfo{
		Error: errorMsg,
	}
}

// SetNowMs sets the current timestamp.
func (dc *DebugCollector) SetNowMs(nowMs int64) {
	dc.nowMs = nowMs
}

// SetTraceID Set trace ID.
func (dc *DebugCollector) SetTraceID(traceID string) {
	dc.traceID = traceID
}

// AddWarning adds warning information.
func (dc *DebugCollector) AddWarning(warning string) {
	dc.warnings = append(dc.warnings, warning)
}

// BuildDebugInfo builds the final Debug information.
func (dc *DebugCollector) BuildDebugInfo() *interfaces.ResolveDebugInfo {
	// Merge property_types, agent_requests, agent_responses into agent_info.
	agentInfo := make(map[string]*interfaces.AgentInfo)
	for propertyName, propertyType := range dc.propertyTypes {
		var request interfaces.AgentRequestDebugInfo
		if req, exists := dc.agentRequests[propertyName]; exists {
			request = req
		}
		agentInfo[propertyName] = &interfaces.AgentInfo{
			PropertyType: propertyType,
			Request:      request,
			Response:     dc.agentResponses[propertyName],
		}
	}

	return &interfaces.ResolveDebugInfo{
		DynamicParams: dc.dynamicParams,
		AgentInfo:     agentInfo,
		NowMs:         dc.nowMs,
		Warnings:      dc.warnings,
		TraceID:       dc.traceID,
	}
}
