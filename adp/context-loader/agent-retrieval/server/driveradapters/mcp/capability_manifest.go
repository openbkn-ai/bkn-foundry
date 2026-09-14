// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	msdk "github.com/mark3labs/mcp-go/mcp"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/extension/mcptool"
)

const (
	capabilityManifestID      = "openbkn.context-loader.mcp"
	capabilityManifestVersion = "0.1.5"

	capabilityResolutionMatched       = "matched"
	capabilityResolutionExecutionOnly = "execution_only"
)

// CapabilityManifest is the versioned, internal Trace contract for the tools
// assembled by Context Loader. It does not alter the public MCP schemas.
type CapabilityManifest struct {
	ManifestID string               `json:"manifest_id"`
	Version    string               `json:"version"`
	Provider   string               `json:"provider"`
	Tools      []CapabilityContract `json:"tools"`
}

func capabilityProfileJSON(toolName string) json.RawMessage {
	input, output := capabilityToolSchemas(loadMCPLocaleBundle(defaultMCPLocale), toolName)
	profile := resolveCapabilityProfile(mustLoadCapabilityManifest(), msdk.Tool{
		Name: toolName, RawInputSchema: input, RawOutputSchema: output,
	})
	raw, err := sonic.ConfigStd.Marshal(profile)
	if err != nil {
		panic("marshal capability profile: " + err.Error())
	}
	return raw
}

// capabilityToolSchemas is the exact schema surface advertised to a licensed
// caller. The manifest must digest this same surface, otherwise an allowed
// decorator appears as an unrecognised capability.
func capabilityToolSchemas(locale *mcpLocaleBundle, toolName string) (json.RawMessage, json.RawMessage) {
	input, output := tryLoadToolSchemas(locale, toolName)
	if decorator, ok := mcptool.DecoratorFor(toolName); ok && decorator.Allowed() {
		input = decorator.Patch(input)
	}
	if len(input) == 0 {
		for _, extra := range mcptool.Extras() {
			if extra.Name == toolName {
				return offerBKNContext(extra.Input), extra.Output
			}
		}
	}
	return input, output
}

// CapabilityContract describes when one exact tool schema may produce
// evidence. A name match with a different schema is deliberately insufficient.
type CapabilityContract struct {
	CanonicalToolName   string   `json:"canonical_tool_name"`
	ToolVersion         string   `json:"tool_version"`
	InputSchemaDigest   string   `json:"input_schema_digest"`
	OutputSchemaDigest  string   `json:"output_schema_digest"`
	ExecutionRole       string   `json:"execution_role"`
	EvidenceContract    string   `json:"evidence_contract"`
	ChildEvidencePolicy string   `json:"child_evidence_policy"`
	MapperID            string   `json:"mapper_id"`
	MapperVersion       string   `json:"mapper_version"`
	MinimumTraceSchema  string   `json:"minimum_trace_schema"`
	RequiredTraceFields []string `json:"required_trace_fields"`
	FailurePolicy       string   `json:"failure_policy"`
}

// CapabilityProfile is attached to an operation capture. Resolution records
// whether the exact manifest entry matched, so consumers never have to guess.
type CapabilityProfile struct {
	ManifestID          string   `json:"manifest_id"`
	ManifestVersion     string   `json:"manifest_version"`
	CanonicalToolName   string   `json:"canonical_tool_name"`
	ToolVersion         string   `json:"tool_version"`
	InputSchemaDigest   string   `json:"input_schema_digest"`
	OutputSchemaDigest  string   `json:"output_schema_digest"`
	ExecutionRole       string   `json:"execution_role"`
	EvidenceContract    string   `json:"evidence_contract"`
	ChildEvidencePolicy string   `json:"child_evidence_policy"`
	MapperID            string   `json:"mapper_id,omitempty"`
	MapperVersion       string   `json:"mapper_version,omitempty"`
	MinimumTraceSchema  string   `json:"minimum_trace_schema,omitempty"`
	RequiredTraceFields []string `json:"required_trace_fields,omitempty"`
	FailurePolicy       string   `json:"failure_policy"`
	Resolution          string   `json:"resolution"`
	Reason              string   `json:"reason,omitempty"`
}

type capabilitySpec struct {
	name, role, evidence, children, mapper string
}

func mustLoadCapabilityManifest() CapabilityManifest {
	specs := capabilitySpecs()
	tools := make([]CapabilityContract, 0, len(specs))
	locale := loadMCPLocaleBundle(defaultMCPLocale)
	for _, spec := range specs {
		input, output := capabilityToolSchemas(locale, spec.name)
		tools = append(tools, CapabilityContract{
			CanonicalToolName:   spec.name,
			ToolVersion:         serverVersion,
			InputSchemaDigest:   schemaDigest(input),
			OutputSchemaDigest:  schemaDigest(output),
			ExecutionRole:       spec.role,
			EvidenceContract:    spec.evidence,
			ChildEvidencePolicy: spec.children,
			MapperID:            spec.mapper,
			MapperVersion:       "1.0.0",
			MinimumTraceSchema:  "3.0.0",
			RequiredTraceFields: requiredTraceFields(spec.evidence),
			FailurePolicy:       "preserve_execution_and_downgrade",
		})
	}
	for _, extra := range mcptool.Extras() {
		tools = append(tools, CapabilityContract{
			CanonicalToolName:   extra.Name,
			ToolVersion:         serverVersion,
			InputSchemaDigest:   schemaDigest(offerBKNContext(extra.Input)),
			OutputSchemaDigest:  schemaDigest(extra.Output),
			ExecutionRole:       "extension",
			EvidenceContract:    "execution_only",
			ChildEvidencePolicy: "managed_children_only",
			MapperID:            "none",
			MapperVersion:       "1.0.0",
			MinimumTraceSchema:  "3.0.0",
			RequiredTraceFields: requiredTraceFields("execution_only"),
			FailurePolicy:       "preserve_execution_and_downgrade",
		})
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].CanonicalToolName < tools[j].CanonicalToolName })
	return CapabilityManifest{
		ManifestID: capabilityManifestID,
		Version:    capabilityManifestVersion,
		Provider:   serverName,
		Tools:      tools,
	}
}

func resolveCapabilityProfile(manifest CapabilityManifest, tool msdk.Tool) CapabilityProfile {
	inputDigest := schemaDigest(tool.RawInputSchema)
	outputDigest := schemaDigest(tool.RawOutputSchema)
	fallback := CapabilityProfile{
		ManifestID:          manifest.ManifestID,
		ManifestVersion:     manifest.Version,
		CanonicalToolName:   tool.Name,
		ToolVersion:         "unknown",
		InputSchemaDigest:   inputDigest,
		OutputSchemaDigest:  outputDigest,
		ExecutionRole:       "unknown",
		EvidenceContract:    "execution_only",
		ChildEvidencePolicy: "none",
		FailurePolicy:       "preserve_execution_and_downgrade",
		Resolution:          capabilityResolutionExecutionOnly,
		Reason:              "capability_not_registered",
	}
	for _, contract := range manifest.Tools {
		if contract.CanonicalToolName != tool.Name {
			continue
		}
		if contract.InputSchemaDigest != inputDigest || contract.OutputSchemaDigest != outputDigest {
			fallback.ExecutionRole = contract.ExecutionRole
			fallback.ToolVersion = contract.ToolVersion
			fallback.Reason = "schema_digest_mismatch"
			return fallback
		}
		return CapabilityProfile{
			ManifestID:          manifest.ManifestID,
			ManifestVersion:     manifest.Version,
			CanonicalToolName:   contract.CanonicalToolName,
			ToolVersion:         contract.ToolVersion,
			InputSchemaDigest:   inputDigest,
			OutputSchemaDigest:  outputDigest,
			ExecutionRole:       contract.ExecutionRole,
			EvidenceContract:    contract.EvidenceContract,
			ChildEvidencePolicy: contract.ChildEvidencePolicy,
			MapperID:            contract.MapperID,
			MapperVersion:       contract.MapperVersion,
			MinimumTraceSchema:  contract.MinimumTraceSchema,
			RequiredTraceFields: append([]string(nil), contract.RequiredTraceFields...),
			FailurePolicy:       contract.FailurePolicy,
			Resolution:          capabilityResolutionMatched,
		}
	}
	return fallback
}

func schemaDigest(raw json.RawMessage) string {
	canonical := []byte("null")
	if len(raw) > 0 {
		var value any
		if json.Unmarshal(raw, &value) == nil {
			if normalized, err := json.Marshal(value); err == nil {
				canonical = normalized
			}
		} else {
			canonical = []byte(strings.TrimSpace(string(raw)))
		}
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func requiredTraceFields(evidence string) []string {
	base := []string{"interaction_id", "operation_id", "attempt", "tool_name", "receipt"}
	if evidence == "execution_only" {
		return base
	}
	return append(base, "business_refs", "result_completeness")
}

func capabilitySpecs() []capabilitySpec {
	return []capabilitySpec{
		{toolKeyStartInteraction, "lifecycle", "execution_only", "none", "none"},
		{toolKeyFinishInteraction, "lifecycle", "execution_only", "none", "none"},
		{toolKeySearchSchema, "discovery", "execution_only", "none", "none"},
		{toolKeySearchInstance, "semantic_query", "ontology_retrieval/v1", "cover_physical_descendants", "ontology_retrieval/v1"},
		{toolKeyQueryObjectInstance, "semantic_query", "ontology_result/v1", "cover_physical_descendants", "ontology_result/v1"},
		{toolKeyQueryInstanceSubgraph, "semantic_query", "ontology_subgraph/v1", "cover_physical_descendants", "ontology_subgraph/v1"},
		{toolKeyExploreSubgraph, "semantic_query", "ontology_subgraph/v1", "cover_physical_descendants", "ontology_subgraph/v1"},
		{toolKeyGetLogicPropertiesValues, "semantic_query", "execution_only", "none", "none"},
		{toolKeyQueryMetric, "semantic_query", "ontology_metric_result/v1", "cover_physical_descendants", "ontology_metric_result/v1"},
		{toolKeyGetActionInfo, "discovery", "execution_only", "none", "none"},
		{toolKeyExecuteAction, "business_function", "execution_only", "managed_children_only", "none"},
		{toolKeyGetActionExecution, "business_function", "execution_only", "none", "none"},
		{toolKeyListActionExecutions, "discovery", "execution_only", "none", "none"},
		{toolKeyListKnowledgeNetworks, "discovery", "execution_only", "none", "none"},
		{toolKeyGetKnDetail, "discovery", "execution_only", "none", "none"},
		{toolKeyGetObjectTypes, "discovery", "execution_only", "none", "none"},
		{toolKeyGetRelationTypes, "discovery", "execution_only", "none", "none"},
		{toolKeyRunSQL, "semantic_query", "mapped_sql_result/v1", "cover_physical_descendants", "mapped_sql_result/v1"},
		{toolKeyRunCypher, "semantic_query", "semantic_query_descriptor/v1", "cover_physical_descendants", "semantic_query_descriptor/v1"},
		{toolKeyListResources, "discovery", "execution_only", "none", "none"},
		{toolKeyDescribeResource, "discovery", "execution_only", "none", "none"},
		{toolKeyListSkills, "discovery", "execution_only", "none", "none"},
		{toolKeyGetSkillContent, "discovery", "execution_only", "none", "none"},
		{toolKeyReadSkillFile, "discovery", "execution_only", "none", "none"},
		{toolKeyExecuteSkill, "orchestrator", "execution_only", "managed_children_only", "none"},
		{toolKeySearchCapabilities, "discovery", "execution_only", "none", "none"},
		{toolKeyExecuteTool, "business_function", "execution_only", "managed_children_only", "none"},
		{toolKeyRunCode, "orchestrator", "execution_only", "managed_children_only", "none"},
		{toolKeyRunShell, "orchestrator", "execution_only", "managed_children_only", "none"},
	}
}
