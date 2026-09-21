// Copyright 2026 openbkn.ai
// Copyright The openbkn.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/extension/mcptool"
)

// longTailTargets are the native tools the compact profile reaches only
// through its gateway (search, describe, execute). Each one is admitted
// because it reads and has no risk of its own that a host would want to
// approve per tool: no writes, no action execution, no credentials, no
// resource-level access outside the knowledge network. Enterprise tools are
// never admitted here without a separate review.
var longTailTargets = []string{
	toolKeyQueryInstanceSubgraph,
	toolKeyExploreSubgraph,
	toolKeyGetLogicPropertiesValues,
	toolKeyGetActionInfo,
	toolKeyGetActionExecution,
	toolKeyListActionExecutions,
	toolKeyGetObjectTypes,
	toolKeyGetRelationTypes,
	toolKeyListSkills,
	toolKeyGetSkillContent,
	toolKeyReadSkillFile,
}

// notInProfileTools are public community tools the compact profile
// deliberately leaves out. Naming one is a caller error with a known fix (use
// the full entry), so the gateway says so instead of pretending it does not
// exist: their names are already public, and telling them apart leaks nothing.
var notInProfileTools = []string{
	toolKeyRunSQL,
	toolKeyRunCypher,
	toolKeyRunCode,
	toolKeyRunShell,
	toolKeyExecuteAction,
	toolKeyExecuteTool,
	toolKeyExecuteSkill,
	toolKeyListResources,
	toolKeyDescribeResource,
	toolKeySearchCapabilities,
}

// gatewayManagedFields are the argument fields the gateway supplies itself,
// so they are not part of what a caller fills in for a target.
var gatewayManagedFields = []string{"bkn_context", "response_format"}

// nativeCatalog resolves long-tail targets against what the builder
// assembled. Resolution happens per call through the builder's own filter, so
// a licence change takes effect on the gateway exactly as it does on
// tools/list, and a stale search result cannot reach a target the licence no
// longer covers.
type nativeCatalog struct {
	builder *toolBuilder
	targets map[string]pendingTool
}

func newNativeCatalog(b *toolBuilder, names []string) *nativeCatalog {
	allowed := toolNameSet(names)
	targets := make(map[string]pendingTool, len(names))
	for _, p := range b.pending {
		if _, ok := allowed[p.tool.Name]; ok {
			targets[p.tool.Name] = p
		}
	}
	return &nativeCatalog{builder: b, targets: targets}
}

// lookup returns the effective definition of an admitted target and its
// handler. ok is false for anything not admitted, not assembled, or not
// covered by the licence at this moment.
func (c *nativeCatalog) lookup(ctx context.Context, name string) (mcp.Tool, mcptool.Handler, bool) {
	target, ok := c.targets[name]
	if !ok {
		return mcp.Tool{}, nil, false
	}
	visible := c.builder.filter(ctx, []mcp.Tool{target.tool})
	if len(visible) == 0 {
		return mcp.Tool{}, nil, false
	}
	return visible[0], target.handler, true
}

// executableSchema is the arguments schema a caller of the gateway fills in
// for a target: the target's effective input schema without the fields the
// gateway supplies itself. describe_native_tool returns exactly this and
// execute_native_read_tool validates against exactly this, so the two cannot
// disagree.
func executableSchema(input json.RawMessage) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	var schema map[string]any
	if err := decoder.Decode(&schema); err != nil {
		return nil, fmt.Errorf("decode input schema: %w", err)
	}
	managed := toolNameSet(gatewayManagedFields)
	if properties, ok := schema["properties"].(map[string]any); ok {
		for field := range managed {
			delete(properties, field)
		}
	}
	if required, ok := schema["required"].([]any); ok {
		kept := make([]any, 0, len(required))
		for _, field := range required {
			if name, isString := field.(string); isString {
				if _, isManaged := managed[name]; isManaged {
					continue
				}
			}
			kept = append(kept, field)
		}
		schema["required"] = kept
	}
	return json.Marshal(schema)
}

// compileExecutableSchema compiles an executable schema for validating a
// caller's arguments. Numbers are kept as written, so a wide integer bound is
// not rounded on the way in. Draft 7 matches the tool schemas and the
// lifecycle validator.
func compileExecutableSchema(schema json.RawMessage) (*jsonschema.Schema, error) {
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(schema))
	if err != nil {
		return nil, fmt.Errorf("decode executable schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft7)
	if err := compiler.AddResource("schema.json", document); err != nil {
		return nil, fmt.Errorf("register executable schema: %w", err)
	}
	return compiler.Compile("schema.json")
}
