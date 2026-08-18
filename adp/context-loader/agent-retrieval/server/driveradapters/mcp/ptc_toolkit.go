// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// PTC (Coded Tool Call) toolkit: renders the tool surface of this service into "a description + a stub".
//
// The client only gives the model a run_code tool. The model writes Python code that is executed in the sandbox
// and calls the functions generated here directly. Intermediate results stay in the sandbox; only stdout returns to the context.
//
// Both artifacts are rendered from the BuildMCPInfo tool catalog, the same source as tools/list. Conditionally
// registered tools and tier-decorated parameters therefore match the actual callable tool surface exactly.
//
// Rendered by the server instead of the client, there are two things that the client cannot do:
// 1. There is no bkn_context in the schema (it is obtained by the lifecycle guard from the business tool at runtime),
// A client rendered from tools/list would put it into the signature as a required parameter and ask the model
// to fill a value it does not have;
// 2. Only the server knows which tools are actually registered.
type PTCToolkit struct {
	// Version is the content hash of digest and stub, which is cached by the client.
	Version string `json:"version"`
	// Digest is a list of function signatures shown to the model and is used as a tool description for run_code.
	Digest string `json:"digest"`
	// A stub is a sandboxed Python implementation that is inlined into the script with each execution.
	Stub string `json:"stub"`
	// SandboxMCPURL is the address the sandbox uses to call back into this service. The sandbox is inside the cluster
	// and cannot use the browser-side gateway address; only the server knows the in-cluster address.
	SandboxMCPURL string `json:"sandbox_mcp_url"`
	// Tools is the full list of tools to be exposed to the model. Clients should iterate through it to build tools, not hard-code by name:
	// This keeps future tool additions server-side only. Digest/Stub are the expanded run_code artifacts and remain
	// as top-level fields only for clients that shipped before Tools existed.
	Tools []PTCTool `json:"tools"`
}

// PTCTool A tool to be exposed to the model. The client builds tools and assembles execution requests accordingly.
type PTCTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
	// Language is directly filled in the language field of the execution factory /function/execute.
	// The sandbox control surface only recognizes python/javascript/shell, and bash will be rejected with 422.
	Language string `json:"language"`
	// Wrap explains how the client should assemble the model's input parameters into code:
	// handler - take the stub and spell "def handler(event):", and indent the model code into the function body.
	// Credentials and bkn_context are sent via event (the existing run_code logic is used).
	// cd_workdir - put a line before the command given by the model and cd to the working directory of this conversation.
	// New values must be synchronized with the client, so the value set is deliberately kept very small.
	Wrap string `json:"wrap"`
}

const (
	// Two tool names exposed by PTC. When merging them into the business tool surface, select run_code by name
	// and replace its description. Keeping literals here avoids scattered string updates.
	toolKeyRunCode  = "run_code"
	toolKeyRunShell = "run_shell"

	// ptcWrapHandler See PTCTool.Wrap.
	ptcWrapHandler = "handler"
	// ptcWrapCdWorkdir See PTCTool.Wrap.
	ptcWrapCdWorkdir = "cd_workdir"
)

// Lifecycle tools are controlled by the caller per turn. The sandbox reuses the same interaction and does not open or close it itself.
// Otherwise, a task will be split into two unrelated evidence chains.
var ptcSkipTools = map[string]bool{
	"bkn_start_interaction":  true,
	"bkn_finish_interaction": true,
}

// ptcToolSchemas takes the input and output parameter declarations of run_code / run_shell.
//
// This reads PTC's own locale resource instead of placing another copy under schemas/: storing the same schema in
// two places makes it easy for model-facing declarations to drift from execution-side behavior without any error.
// The returned input schema already includes bkn_context: run_code really needs that parameter, and offerBKNContext
// is exactly how business tools add it. Sharing the same function keeps the two paths byte-for-byte consistent.
func ptcToolSchemas(locale *mcpLocaleBundle, toolKey string) (json.RawMessage, json.RawMessage, bool) {
	var inputResource string
	switch toolKey {
	case toolKeyRunCode:
		inputResource = "ptc_run_code_schema.json"
	case toolKeyRunShell:
		inputResource = "ptc_run_shell_schema.json"
	default:
		return nil, nil, false
	}
	return offerBKNContext(json.RawMessage(locale.PTCResource(inputResource))),
		json.RawMessage(locale.PTCResource("ptc_output_schema.json")), true
}

// ptcInlineDescriptions describes the PTC tool when rendering and entering the business tool surface, returned by tool key.
//
// tools is the complete list of tools in the current version (including PTC tools themselves, excluded by ptcUsableTools).
// /mcp tools/list and /mcp/info both use this source so they cannot diverge.
func ptcInlineDescriptions(locale *mcpLocaleBundle, tools []MCPToolInfo) map[string]string {
	usable := ptcUsableTools(&MCPInfo{Tools: tools})
	return map[string]string{
		toolKeyRunCode:  renderPTCDigestForLocale(locale, usable, false),
		toolKeyRunShell: locale.PTCResource("ptc_run_shell_description.txt"),
	}
}

// The PTC tool cannot list itself in the digest: that list is a list of callable functions for scripts in the sandbox,
// Writing run_code is equivalent to telling the model that it can open another layer of sandbox in the code.
var ptcSelfTools = map[string]bool{toolKeyRunCode: true, toolKeyRunShell: true}

// bkn_context is the session lifecycle pipeline, which is automatically injected by stub's _call and should not appear in the signature.
var ptcPlumbingParams = map[string]bool{"bkn_context": true}

// Schema defaults to response_format=toon, a token-saving text format optimized for "directly feeding the model".
// In code mode, the return value is first processed by the script and needs a subscriptable structure, so it is overridden to json.
var ptcDefaultOverrides = map[string]any{"response_format": "json"}

var ptcPyTypes = map[string]string{
	"string": "str", "array": "list", "object": "dict",
	"boolean": "bool", "integer": "int", "number": "float",
}

const defaultSandboxMCPPath = "/api/agent-retrieval/v1/mcp/"

// sandboxMCPURL returns the address of the sandbox return visit to this service.
//
// The trailing slash cannot be omitted: the gateway will jump to 307 when the slash is missing, and the sandbox side uses the standard library urllib, which is not correct for POST.
// Follow the redirect - the symptom is a 400 with no packet, and the troubleshooting cost is much greater than this line of comments.
func sandboxMCPURL(port int) string {
	if v := strings.TrimSpace(os.Getenv("PTC_SANDBOX_MCP_URL")); v != "" {
		if !strings.HasSuffix(v, "/") {
			v += "/"
		}
		return v
	}
	host := strings.TrimSpace(os.Getenv("PTC_SANDBOX_MCP_HOST"))
	if host == "" {
		host = "agent-retrieval"
	}
	return fmt.Sprintf("http://%s:%d%s", host, port, defaultSandboxMCPPath)
}

type ptcParam struct {
	name     string
	pyType   string
	required bool
	defVal   any
	desc     string
}

func ptcParams(raw json.RawMessage) []ptcParam {
	var schema struct {
		Properties map[string]struct {
			Type        string `json:"type"`
			Default     any    `json:"default"`
			Description string `json:"description"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &schema) != nil {
		return nil
	}
	required := make(map[string]bool, len(schema.Required))
	for _, r := range schema.Required {
		required[r] = true
	}
	// The JSON objects are unordered and sorted by name to ensure that the bytes are consistent between the two renderings - Version is the content hash.
	// Sequence thrashing will cause the client to think that the tool surface has changed every time.
	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		if !ptcPlumbingParams[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	params := make([]ptcParam, 0, len(names))
	for _, name := range names {
		spec := schema.Properties[name]
		def := spec.Default
		if override, ok := ptcDefaultOverrides[name]; ok {
			def = override
		}
		pyType, ok := ptcPyTypes[spec.Type]
		if !ok {
			pyType = "object"
		}
		params = append(params, ptcParam{
			name: name, pyType: pyType, required: required[name],
			defVal: def, desc: spec.Description,
		})
	}
	// Required first: Python does not allow parameters with default values to be listed before parameters without default values.
	sort.SliceStable(params, func(i, j int) bool { return params[i].required && !params[j].required })
	return params
}

func pyLiteral(v any) string {
	switch t := v.(type) {
	case nil:
		return "None"
	case bool:
		if t {
			return "True"
		}
		return "False"
	case string:
		return "'" + strings.ReplaceAll(t, "'", "\\'") + "'"
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%v", t)
	default:
		return fmt.Sprintf("%v", t)
	}
}

func ptcSignature(tool MCPToolInfo) string {
	parts := make([]string, 0, 8)
	for _, p := range ptcParams(tool.InputSchema) {
		if p.required {
			parts = append(parts, fmt.Sprintf("%s: %s", p.name, p.pyType))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s: %s = %s", p.name, p.pyType, pyLiteral(p.defVal)))
	}
	return fmt.Sprintf("%s(%s)", tool.Name, strings.Join(parts, ", "))
}

// ptcReturnKeys Render return value top-level keys. Key names are not uniform among tools (some list classes are called entries,
// Some are called data), and the model cannot be inferred - if it is not written out, the first call will fail with a KeyError.
// ptcReturnKeys renders the return structure, and the array keys expand one layer of element fields downwards.
//
// Just writing the top level key is not enough. In code mode, the caller must first write the value path before executing it. If it cannot be obtained, it must.
// Spend an extra round to type out the original structure to find the field names - in actual testing, the object_types of search_schema was therefore.
// As if there is a name field (actually concept_name), a whole round is wasted on probing, and the probing itself requires.
// The original data is printed back to the context, which just offsets the context-saving meaning of the code pattern.
//
// Expand only one layer: the deeper it is, the signature list will be expanded into the full text of the schema, and the second layer down can be in the script.
// help() or inspect the value directly.
func ptcReturnKeys(tool MCPToolInfo) string {
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if len(tool.OutputSchema) == 0 || json.Unmarshal(tool.OutputSchema, &schema) != nil {
		return "dict"
	}
	if len(schema.Properties) == 0 {
		return "dict"
	}

	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	sort.Strings(names)

	rendered := make([]string, 0, len(names))
	for _, name := range names {
		if inner := ptcItemKeys(schema.Properties[name]); inner != "" {
			rendered = append(rendered, name+"["+inner+"]")
			continue
		}
		rendered = append(rendered, name)
	}
	return "{" + strings.Join(rendered, ", ") + "}"
}

// ptcItemKeys takes the field name of the array element. If it is not an array, or the element has no declared field, an empty string is returned.
// The caller presses normal keys to render.
func ptcItemKeys(raw json.RawMessage) string {
	var field struct {
		Type  string `json:"type"`
		Items struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"items"`
	}
	if json.Unmarshal(raw, &field) != nil || field.Type != "array" {
		return ""
	}
	if len(field.Items.Properties) == 0 {
		return ""
	}
	names := make([]string, 0, len(field.Items.Properties))
	for name := range field.Items.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, " ")
}

// ptcToolNames Lines up the tool names for use with the unsigned version of the digest.
// Sorting ensures repeatable rendering - Version is a content hash, and order jittering will make the client think that the tool surface has changed every time.
func ptcToolNames(tools []MCPToolInfo) string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		if tool.Name != "" {
			names = append(names, tool.Name)
		}
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func ptcUsableTools(info *MCPInfo) []MCPToolInfo {
	tools := make([]MCPToolInfo, 0, len(info.Tools))
	for _, t := range info.Tools {
		if t.Name == "" || ptcSkipTools[t.Name] || ptcSelfTools[t.Name] {
			continue
		}
		tools = append(tools, t)
	}
	// Groups are arranged according to the smallest Order within the group, rather than group name dictionary order: Order encodes "discover first, then query,
	// "Execute later", the dictionary order will disrupt it.
	groupRank := map[string]int{}
	for _, t := range tools {
		g := ptcGroupOf(t)
		if cur, ok := groupRank[g]; !ok || t.Order < cur {
			groupRank[g] = t.Order
		}
	}
	sort.SliceStable(tools, func(i, j int) bool {
		gi, gj := ptcGroupOf(tools[i]), ptcGroupOf(tools[j])
		if gi != gj {
			return groupRank[gi] < groupRank[gj]
		}
		return tools[i].Order < tools[j].Order
	})
	return tools
}

func ptcGroupOf(t MCPToolInfo) string {
	if t.GroupTitle != "" {
		return t.GroupTitle
	}
	if t.Group != "" {
		return t.Group
	}
	return "other"
}

func ptcGroupTitle(locale *mcpLocaleBundle, tool MCPToolInfo) string {
	if group := ptcGroupOf(tool); group != "other" {
		return group
	}
	return locale.PTCResource("ptc_group_other.txt")
}

func renderPTCDigest(tools []MCPToolInfo) string {
	return renderPTCDigestForLocale(loadMCPLocaleBundle(defaultMCPLocale), tools, true)
}

// renderPTCDigestForLocale renders the tool description for run_code.
//
// withSignatures determines whether to bring the function signature list, which accounts for 55% of the entire digest:
//
// - The tool surface on the independent PTC endpoint only has run_code / run_shell, and the model has no other place to look at, so it must be brought;
// - run_code is not included when listed alongside business tools on the same tool surface - the complete schema for those tools is in.
// In the tool interface, rendering the Python signature again is equivalent to describing the same batch of tools twice.
//
// Use another preface without a signature: it does not list functions, it only states that "the tool on the tool surface is already in scope" and.
// Two inconsistencies with the schema (bkn_context is injected by runtime, response_format fixes json).
// Those two items cannot be dismissed by "parameters are consistent with the schema" - bkn_context in the schema is required.
func renderPTCDigestForLocale(locale *mcpLocaleBundle, tools []MCPToolInfo, withSignatures bool) string {
	var b strings.Builder
	if !withSignatures {
		b.WriteString(locale.PTCResource("ptc_digest_prefix_inline.txt"))
		// List only names, not signatures. The parameters and return values allow the caller to read the schema on the tool surface - that's right there.
		// At present, repeated rendering is a waste; but "which tools can be called in the script" should not be inferred: conditionally registered tools.
		// (execute_skill) exists or not, and whether any one is missing, there will be no ambiguity if it is listed.
		// The measured 21 names are about 300 characters, while the full signature list is 4897.
		if names := ptcToolNames(tools); names != "" {
			b.WriteString("\n")
			b.WriteString(locale.PTCResource("ptc_digest_names_lead.txt"))
			b.WriteString(names)
			// One more line break: the adjacent suffix starts with the ## title, and markdown does not work if it is stuck in the same paragraph.
			b.WriteString("\n\n")
		}
		b.WriteString(locale.PTCResource("ptc_digest_suffix.txt"))
		return b.String()
	}

	b.WriteString(locale.PTCResource("ptc_digest_prefix.txt"))
	group := ""
	for _, tool := range tools {
		if next := ptcGroupOf(tool); next != group {
			if group != "" {
				b.WriteString("```\n")
			}
			fmt.Fprintf(&b, "\n### %s\n\n```python\n", ptcGroupTitle(locale, tool))
			group = next
		}
		fmt.Fprintf(&b, "%s -> %s\n", ptcSignature(tool), ptcReturnKeys(tool))
		if tool.Title != "" {
			fmt.Fprintf(&b, "    # %s\n", tool.Title)
		}
		for _, hint := range locale.PTCHints(tool.Name) {
			fmt.Fprintf(&b, "    #   %s\n", hint)
		}
	}
	if group != "" {
		b.WriteString("```\n")
	}
	b.WriteString(locale.PTCResource("ptc_digest_suffix.txt"))
	return b.String()
}

// escapePyDocstring makes schema text safe to paste into a `"""..."""` literal.
//
// The text comes from the tool schemas, where a backslash is ordinary prose - the like
// contract writes `\%` to mean an escaped percent sign. Pasted verbatim it becomes an
// invalid Python escape sequence, which is a SyntaxWarning from 3.12 on, and a literal
// `"""` in a description would end the docstring and break the whole stub.
func escapePyDocstring(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `"""`, `\"\"\"`)
}

func renderPTCStub(tools []MCPToolInfo) string {
	var b strings.Builder
	b.WriteString(mustReadMCPStaticResource("ptc_stub.py"))
	for _, t := range tools {
		params := ptcParams(t.InputSchema)
		fmt.Fprintf(&b, "\n\ndef %s -> dict:\n", ptcSignature(t))
		b.WriteString(`    """` + escapePyDocstring(strings.TrimSpace(t.Description)) + "\n")
		for _, p := range params {
			if p.desc != "" {
				fmt.Fprintf(&b, "\n    %s: %s\n", p.name, escapePyDocstring(strings.TrimSpace(p.desc)))
			}
		}
		b.WriteString(`    """` + "\n")
		args := make([]string, 0, len(params))
		for _, p := range params {
			args = append(args, fmt.Sprintf("%q: %s", p.name, p.name))
		}
		fmt.Fprintf(&b, "    return _call(%q, {%s})\n", t.Name, strings.Join(args, ", "))
	}
	b.WriteString("\n\n__all__ = [\n")
	for _, t := range tools {
		fmt.Fprintf(&b, "    %q,\n", t.Name)
	}
	b.WriteString("    \"ToolError\",\n]\n")
	return b.String()
}

// BuildPTCToolkit renders the PTC toolkit. endpoint is consistent with BuildMCPInfo (self-describing only),
// port is the listening port of this service and is used to derive the sandbox return address.
func BuildPTCToolkit(endpoint string, port int) (*PTCToolkit, error) {
	return BuildPTCToolkitForLocale(endpoint, port, defaultMCPLocale)
}

// BuildPTCToolkitForLocale renders the PTC toolkit from the effective locale.
func BuildPTCToolkitForLocale(endpoint string, port int, locale string) (*PTCToolkit, error) {
	info, err := BuildMCPInfoForLocale(endpoint, locale)
	if err != nil {
		return nil, err
	}
	return buildPTCToolkitFromLocale(ptcUsableTools(info), port, loadMCPLocaleBundle(locale))
}

// InlinePTCToolkit renders the PTC toolkit (run_code / run_shell) that can be incorporated into the business tool surface.
//
// The only difference from BuildPTCToolkit is the description of run_code: the signature list is omitted, because when paralleling.
// The complete schema of those tools is on the same tool surface, rendering the Python signature again is a duplication.
// Measured two renderings 8852 vs about 3800 characters.
//
// The rest (stub, sandbox return address, assembly method) are exactly the same - the two paths eventually spell out the same script.
//
// Return the entire toolkit instead of just the tool table: Stub and SandboxMCPURL are required on the execution side to build the script.
// The same build path is followed instead of changing the description afterwards, so Version covers the actual content of this version.
// There will never be two different digests with the same hash.
func InlinePTCToolkit(port int, locale string) (*PTCToolkit, error) {
	info, err := BuildMCPInfoForLocale("", locale)
	if err != nil {
		return nil, err
	}
	return buildPTCToolkitVariant(ptcUsableTools(info), port, loadMCPLocaleBundle(locale), false)
}

// buildPTCToolkitFrom Renders a toolkit from a filtered tools directory. Separate from BuildPTCToolkit,
// This is so that the test can cover the tool table and version number without setting up a real endpoint.
func buildPTCToolkitFrom(tools []MCPToolInfo, port int) (*PTCToolkit, error) {
	return buildPTCToolkitFromLocale(tools, port, loadMCPLocaleBundle(defaultMCPLocale))
}

func buildPTCToolkitFromLocale(tools []MCPToolInfo, port int, locale *mcpLocaleBundle) (*PTCToolkit, error) {
	return buildPTCToolkitVariant(tools, port, locale, true)
}

// buildPTCToolkitVariant is the build path shared by both digest renderings. withSignatures decides.
// Whether the function signature list is included in the description of run_code: there are no other tools to refer to on the independent endpoint, it must be included;
// When entering the business tool interface, the schema is next to it, leaving only the function name.
func buildPTCToolkitVariant(
	tools []MCPToolInfo, port int, locale *mcpLocaleBundle, withSignatures bool,
) (*PTCToolkit, error) {
	digest := renderPTCDigestForLocale(locale, tools, withSignatures)
	stub := renderPTCStub(tools)
	exposed := []PTCTool{
		{
			Name:        toolKeyRunCode,
			Description: digest,
			InputSchema: json.RawMessage(locale.PTCResource("ptc_run_code_schema.json")),
			Language:    "python",
			Wrap:        ptcWrapHandler,
		},
		{
			Name:        toolKeyRunShell,
			Description: locale.PTCResource("ptc_run_shell_description.txt"),
			InputSchema: json.RawMessage(locale.PTCResource("ptc_run_shell_schema.json")),
			Language:    "shell",
			Wrap:        ptcWrapCdWorkdir,
		},
	}
	// Version covers the entire tool table: the client caches it, and it will become invalid as long as the tool surface seen by the model changes.
	// Light hash digest+stub will allow changes such as new tools and description changes to silently use the old cache.
	fingerprint, err := json.Marshal(exposed)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(append(append([]byte(digest), 0), append([]byte(stub), fingerprint...)...))
	return &PTCToolkit{
		Version:       "sha256:" + hex.EncodeToString(sum[:]),
		Digest:        digest,
		Stub:          stub,
		SandboxMCPURL: sandboxMCPURL(port),
		Tools:         exposed,
	}, nil
}
