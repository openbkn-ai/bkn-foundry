// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import "github.com/mark3labs/mcp-go/mcp"

// Tool annotations are the protocol's own field for "what does calling this do
// to the world". A host uses them to gate, to order a confirmation prompt, or to
// decide what may run unattended; they are not sent to the model and cost no
// context.
//
// Every hint defaults to the pessimistic answer when absent - not read-only,
// destructive, not idempotent, open world - so leaving them unset tells a host
// that reading a schema is as dangerous as running a shell. Twenty-one of these
// tools only read.
//
// destructiveHint and idempotentHint are defined only for tools that are not
// read-only, so they are set only there. openWorldHint is false wherever a tool
// stays inside one knowledge network or the resources bound to it, and true
// where the reachable set is whatever the caller writes.
var (
	annTrue  = true
	annFalse = false
)

// readOnlyTool annotates a tool that only reads within a bounded domain.
func readOnlyTool() mcp.ToolAnnotation {
	return mcp.ToolAnnotation{ReadOnlyHint: &annTrue, OpenWorldHint: &annFalse}
}

// arbitraryEffectTool annotates a tool whose effect is decided by what the
// caller submits - registered code, a shell line, a business action. The
// pessimistic answer is the correct one for all three hints.
func arbitraryEffectTool() mcp.ToolAnnotation {
	return mcp.ToolAnnotation{
		ReadOnlyHint: &annFalse, DestructiveHint: &annTrue,
		IdempotentHint: &annFalse, OpenWorldHint: &annTrue,
	}
}

// lifecycleTool annotates a managed-session transition. It writes, so it is not
// read-only, but it destroys nothing and never leaves this deployment. Each call
// advances the session, so it is not idempotent either.
func lifecycleTool() mcp.ToolAnnotation {
	return mcp.ToolAnnotation{
		ReadOnlyHint: &annFalse, DestructiveHint: &annFalse,
		IdempotentHint: &annFalse, OpenWorldHint: &annFalse,
	}
}

// toolAnnotations maps every tool key to its effect class. A tool missing from
// here is advertised with the pessimistic defaults, which is safe but wrong for
// a reader; TestEveryToolIsAnnotated fails instead of letting that pass quietly.
var toolAnnotations = map[string]func() mcp.ToolAnnotation{
	// Lifecycle: writes managed session state.
	// Named by literal, as lifecycle_adapter.go does: these two have no key
	// constant, they are registered from lifecycleToolNames.
	"bkn_start_interaction":  lifecycleTool,
	"bkn_finish_interaction": lifecycleTool,

	// Discovery and query: read a knowledge network. run_sql belongs here - it
	// is read-only SQL against resources already bound to the network, and the
	// grammar it accepts excludes every write.
	toolKeyListKnowledgeNetworks:    readOnlyTool,
	toolKeyGetKnDetail:              readOnlyTool,
	toolKeySearchSchema:             readOnlyTool,
	toolKeyGetObjectTypes:           readOnlyTool,
	toolKeyGetRelationTypes:         readOnlyTool,
	toolKeySearchInstance:           readOnlyTool,
	toolKeyQueryObjectInstance:      readOnlyTool,
	toolKeyQueryInstanceSubgraph:    readOnlyTool,
	toolKeyExploreSubgraph:          readOnlyTool,
	toolKeyGetLogicPropertiesValues: readOnlyTool,
	toolKeyRunSQL:                   readOnlyTool,
	toolKeyQueryMetric:              readOnlyTool,
	toolKeyListResources:            readOnlyTool,
	toolKeyDescribeResource:         readOnlyTool,

	// Actions: reading what an action is and what it did is not doing it.
	toolKeyGetActionInfo:        readOnlyTool,
	toolKeyGetActionExecution:   readOnlyTool,
	toolKeyListActionExecutions: readOnlyTool,
	toolKeyExecuteAction:        arbitraryEffectTool,

	// Skills: the catalogue and the files are readable; running one is not.
	toolKeyFindSkills:      readOnlyTool,
	toolKeyListSkills:      readOnlyTool,
	toolKeyGetSkillContent: readOnlyTool,
	toolKeyReadSkillFile:   readOnlyTool,
	toolKeyExecuteSkill:    arbitraryEffectTool,

	// Execution: the caller supplies the program.
	toolKeyRunCode:  arbitraryEffectTool,
	toolKeyRunShell: arbitraryEffectTool,
}

// annotationFor returns the annotation for a tool key, or the zero value when
// the key is unknown - an unannotated tool keeps the protocol defaults rather
// than claiming anything.
func annotationFor(toolKey string) mcp.ToolAnnotation {
	if build, ok := toolAnnotations[toolKey]; ok {
		return build()
	}
	return mcp.ToolAnnotation{}
}
