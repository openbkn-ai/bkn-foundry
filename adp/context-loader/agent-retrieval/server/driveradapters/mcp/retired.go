// Copyright 2026 openbkn.ai
// Copyright The openbkn.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

// retiredTools are assembled but published by no MCP profile.
//
// run_sql and the two resource tools that feed it are the physical query
// surface: they answer with raw columns under the caller's own data-resource
// grants, outside the property-level rules the knowledge network applies to
// every other query tool. They stay on the REST surface (/kn/run_sql,
// /kn/list_resources, /kn/describe_resource), which is what the execution
// factory and other internal callers use; only the model-facing MCP surface
// withholds them.
//
// The tools are still assembled rather than deleted: the capability manifest
// keeps describing them, a decorator registered against one still lands on an
// assembled tool (see verifyDecoratorsLanded), and publishing them again is a
// line removed from this list. Withholding happens in toolBuilder.filter,
// which every profile installs and which mcp-go runs on tools/call as well as
// tools/list, and through which the gateway catalogue resolves every target.
// So /mcp, /mcp-compact and the gateway all refuse a retired tool exactly as
// they refuse a tool that was never built in. /mcp/info leaves them out on its
// own path (see buildMCPInfoForLocale), which is also what keeps them out of
// the sandbox toolkit rendered from it.
var retiredTools = toolNameSet([]string{
	toolKeyRunSQL,
	toolKeyListResources,
	toolKeyDescribeResource,
})
