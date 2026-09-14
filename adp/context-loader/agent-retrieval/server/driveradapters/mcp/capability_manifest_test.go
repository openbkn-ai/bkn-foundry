// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"strings"
	"testing"

	msdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/extension/mcptool"
)

func TestCapabilityManifestCoversTheAssembledRuntimeCatalog(t *testing.T) {
	noExtensions(t)
	manifest := mustLoadCapabilityManifest()

	if manifest.ManifestID != "openbkn.context-loader.mcp" || manifest.Version != "0.1.5" {
		t.Fatalf("manifest identity = %q@%q", manifest.ManifestID, manifest.Version)
	}
	for _, tool := range assembledTools(t) {
		profile := resolveCapabilityProfile(manifest, tool)
		if profile.Resolution != capabilityResolutionMatched {
			t.Errorf("tool %q resolution = %q (%s)", tool.Name, profile.Resolution, profile.Reason)
		}
		if profile.InputSchemaDigest == "" || profile.OutputSchemaDigest == "" {
			t.Errorf("tool %q has incomplete schema identity", tool.Name)
		}
	}
}

func TestCapabilityManifestClassifiesEvidenceBoundaries(t *testing.T) {
	noExtensions(t)
	manifest := mustLoadCapabilityManifest()
	cases := map[string]struct {
		role, contract, children string
	}{
		toolKeyRunCypher:           {"semantic_query", "semantic_query_descriptor/v1", "cover_physical_descendants"},
		toolKeyQueryObjectInstance: {"semantic_query", "ontology_result/v1", "cover_physical_descendants"},
		toolKeyQueryMetric:         {"semantic_query", "ontology_metric_result/v1", "cover_physical_descendants"},
		toolKeyExecuteTool:         {"business_function", "execution_only", "managed_children_only"},
		toolKeyRunCode:             {"orchestrator", "execution_only", "managed_children_only"},
		toolKeySearchSchema:        {"discovery", "execution_only", "none"},
	}
	tools := map[string]msdk.Tool{}
	for _, tool := range assembledTools(t) {
		tools[tool.Name] = tool
	}
	for name, want := range cases {
		profile := resolveCapabilityProfile(manifest, tools[name])
		if profile.ExecutionRole != want.role || profile.EvidenceContract != want.contract || profile.ChildEvidencePolicy != want.children {
			t.Errorf("%s profile = role %q contract %q children %q", name, profile.ExecutionRole, profile.EvidenceContract, profile.ChildEvidencePolicy)
		}
	}
}

func TestCapabilityManifestRequiresBusinessReferencesForMetricEvidence(t *testing.T) {
	noExtensions(t)
	manifest := mustLoadCapabilityManifest()
	tools := map[string]msdk.Tool{}
	for _, tool := range assembledTools(t) {
		tools[tool.Name] = tool
	}

	profile := resolveCapabilityProfile(manifest, tools[toolKeyQueryMetric])
	if profile.MapperID != "ontology_metric_result/v1" || profile.MapperVersion != "1.0.0" {
		t.Fatalf("query_metric mapper = %q@%q", profile.MapperID, profile.MapperVersion)
	}
	wantFields := map[string]bool{"business_refs": true, "result_completeness": true}
	for _, field := range profile.RequiredTraceFields {
		delete(wantFields, field)
	}
	if len(wantFields) != 0 {
		t.Fatalf("query_metric required fields missing: %v", wantFields)
	}
}

func TestCapabilityManifestRejectsChangedAndUnknownTools(t *testing.T) {
	noExtensions(t)
	manifest := mustLoadCapabilityManifest()
	known := assembledTools(t)[0]
	known.RawInputSchema = []byte(`{"type":"object","properties":{"new_field":{"type":"string"}}}`)

	changed := resolveCapabilityProfile(manifest, known)
	if changed.Resolution != capabilityResolutionExecutionOnly || changed.Reason != "schema_digest_mismatch" {
		t.Fatalf("changed schema resolved as %#v", changed)
	}

	unknown := resolveCapabilityProfile(manifest, msdk.Tool{
		Name:            "future_tool",
		RawInputSchema:  []byte(`{"type":"object"}`),
		RawOutputSchema: []byte(`{"type":"object"}`),
	})
	if unknown.Resolution != capabilityResolutionExecutionOnly || unknown.EvidenceContract != "execution_only" || unknown.Reason != "capability_not_registered" {
		t.Fatalf("unknown tool resolved as %#v", unknown)
	}
	if !strings.HasPrefix(unknown.InputSchemaDigest, "sha256:") || !strings.HasPrefix(unknown.OutputSchemaDigest, "sha256:") {
		t.Fatalf("unknown tool did not retain schema identity: %#v", unknown)
	}
}

func TestCapabilityManifestIncludesRegisteredExtensionsAsExecutionOnly(t *testing.T) {
	noExtensions(t)
	extra := extraTool("trace_extension", "trace_extension")
	mcptool.Register(extra)

	manifest := mustLoadCapabilityManifest()
	profile := resolveCapabilityProfile(manifest, msdk.Tool{
		Name: extra.Name, RawInputSchema: offerBKNContext(extra.Input), RawOutputSchema: extra.Output,
	})
	if profile.Resolution != capabilityResolutionMatched || profile.ExecutionRole != "extension" ||
		profile.EvidenceContract != "execution_only" || profile.ChildEvidencePolicy != "managed_children_only" {
		t.Fatalf("registered extension did not receive a safe contract: %#v", profile)
	}
}
