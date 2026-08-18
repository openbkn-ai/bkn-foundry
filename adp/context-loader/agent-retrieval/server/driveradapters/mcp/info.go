// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/extension/mcptool"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knskills"
)

// MCPToolInfo External description of a single tool (name / display metadata / description / input and output schema).
//
// title / group / group_title / order has the same origin as tools/list: where title falls in the protocol.
// On its own field, the other three fall in the `_meta` of the tool, which are all tiled fields. Two places must be given.
// The same answer - the reason for the existence of this endpoint is "to be able to see clearly the capabilities without shaking hands.".
type MCPToolInfo struct {
	Name         string          `json:"name"`
	Title        string          `json:"title,omitempty"`
	Group        string          `json:"group,omitempty"`
	GroupTitle   string          `json:"group_title,omitempty"`
	Order        int             `json:"order,omitempty"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"input_schema,omitempty"`
	OutputSchema json.RawMessage `json:"output_schema,omitempty"`
}

// MCPInfo MCP service self-describing document: endpoint, protocol, authentication, tool directory, client configuration example.
// For Agent/people to learn how to integrate in one go via GET without having to go through the MCP handshake first.
type MCPInfo struct {
	Service             string          `json:"service"`
	Endpoint            string          `json:"endpoint"`
	Protocol            string          `json:"protocol"`
	Transport           string          `json:"transport"`
	Auth                string          `json:"auth"`
	ToolCount           int             `json:"tool_count"`
	Tools               []MCPToolInfo   `json:"tools"`
	ClientConfigExample json.RawMessage `json:"client_config_example"`
}

// tryLoadToolSchemas has the same origin as loadToolSchemas, but returns nil instead of panic when it cannot be read or fails to parse.
// For use by info endpoint fault tolerance. After getting it, stack it one layer according to locale, and have the same answer as tools/list.
func tryLoadToolSchemas(locale *mcpLocaleBundle, toolKey string) (input, output json.RawMessage) {
	if input, output, ok := lifecycleToolSchemas(toolKey); ok {
		return locale.OverlaySchemas(toolKey, input, output)
	}
	if input, output, ok := ptcToolSchemas(locale, toolKey); ok {
		return input, output
	}
	data, err := schemasFS.ReadFile(fmt.Sprintf("schemas/%s.json", toolKey))
	if err != nil {
		return nil, nil
	}
	var wrapper toolSchemaFile
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, nil
	}
	if isBusinessTool(toolKey) {
		wrapper.InputSchema = offerBKNContext(wrapper.InputSchema)
	}
	return locale.OverlaySchemas(toolKey, wrapper.InputSchema, wrapper.OutputSchema)
}

// BuildMCPInfo builds the MCP self-description in the baseline locale.
// Request-facing callers use BuildMCPInfoForLocale.
func BuildMCPInfo(endpoint string) (*MCPInfo, error) {
	return BuildMCPInfoForLocale(endpoint, defaultMCPLocale)
}

// BuildMCPInfoForLocale builds the MCP self-description using the requested
// effective locale. endpoint is the public MCP Streamable HTTP address.
func BuildMCPInfoForLocale(endpoint, localeName string) (*MCPInfo, error) {
	data, err := schemasFS.ReadFile("schemas/tools_meta.json")
	if err != nil {
		return nil, fmt.Errorf("read tools_meta.json: %w", err)
	}
	var meta map[string]ToolMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("parse tools_meta.json: %w", err)
	}
	// Resolve localized text from the same resource bundle as tools/list.

	// Use the same set of judgments based on the current gear as tools/list: decorated tools bring paid parameters,
	// Unauthorized enterprise tools do not appear. If the two places are inconsistent, the endpoint is worse than not existing - its.
	// The purpose is to let people see their capabilities clearly without shaking hands.
	type entry struct {
		key  string
		info MCPToolInfo
	}
	locale := loadMCPLocaleBundle(localeName)
	all := make([]entry, 0, len(meta))
	for key := range meta {
		// Unassembled tools cannot appear here: the purpose of this endpoint is to "see the capabilities without shaking hands",
		// Broadcasting a tools/call entry that will answer "No such tool" is worse than not broadcasting at all.
		if key == toolKeyExecuteSkill && !knskills.ExecuteEnabled() {
			continue
		}
		m := locale.ToolMeta(key)
		in, out := tryLoadToolSchemas(locale, key)
		if d, ok := mcptool.DecoratorFor(key); ok && d.Allowed() {
			in = d.Patch(in)
		}
		all = append(all, entry{key, MCPToolInfo{
			Name:         m.Name,
			Title:        m.Title,
			Group:        m.Group,
			GroupTitle:   m.GroupTitle,
			Order:        m.Order,
			Description:  m.Description,
			InputSchema:  in,
			OutputSchema: out,
		}})
	}
	// Community binary here is an empty loop.
	for _, t := range mcptool.Extras() {
		if !t.Allowed() {
			continue
		}
		all = append(all, entry{t.Key, MCPToolInfo{
			Name:        t.Name,
			Title:       t.Title,
			Group:       t.Group,
			GroupTitle:  t.GroupTitle,
			Order:       t.Order,
			Description: t.Desc,
			// Same as tools/list (addExtras of assemble.go): Enterprise tools are based on this service.
			// Its own definition is a business tool, and the lifecycle guard will ask it for bkn_context. this endpoint.
			// The reason for existence is "to see the capabilities clearly without shaking hands". Broadcasting a schema that cannot be called is better than not broadcasting at all.
			// Even worse - integrating it will get conversation_required directly.
			InputSchema:  offerBKNContext(t.Input),
			OutputSchema: t.Output,
		}})
	}
	// PTC tool descriptions must have the same origin as tools/list. run_code is rendered according to the current tool table.
	// The function list cannot be stored in the static file; if you write one copy in each place, people who integrate it according to /mcp/info will.
	// Get a different description than what the model saw.
	//
	// Here you can render directly without having to go back to adjust InlinePTCToolkit: the adjustable function table is just assembled.
	// all, and ptcUsableTools has excluded the PTC tool itself, and there is no recursion.
	catalogue := make([]MCPToolInfo, 0, len(all))
	for _, e := range all {
		catalogue = append(catalogue, e.info)
	}
	descriptions := ptcInlineDescriptions(locale, catalogue)
	for i := range all {
		if text, ok := descriptions[all[i].key]; ok {
			all[i].info.Description = text
		}
	}

	// Sort by tool key, not by external name. Currently tools_meta.json is the same as locales/en-US.
	// The name here is always equal to the key, and the results of the two arrangements are the same - but that is a coincidence, change the name in the locale.
	// The order on the community side has changed accordingly.
	// Stable sorting: The assembly period has ensured that the keys are non-duplicate (toolBuilder.claimName), and the stable sorting is.
	// The second insurance - when duplication occurs, at least the sequence will not jitter between the two processes.
	sort.SliceStable(all, func(i, j int) bool { return all[i].key < all[j].key })

	tools := make([]MCPToolInfo, 0, len(all))
	for _, e := range all {
		tools = append(tools, e.info)
	}

	cfg, _ := json.Marshal(map[string]any{
		"mcpServers": map[string]any{
			serverName: map[string]any{
				"url": endpoint,
				"headers": map[string]string{
					"Authorization": "Bearer <access-token>",
				},
			},
		},
	})

	return &MCPInfo{
		Service:             serverName,
		Endpoint:            endpoint,
		Protocol:            "MCP / JSON-RPC 2.0 (initialize → tools/list → tools/call)",
		Transport:           "Streamable HTTP",
		Auth:                "Bearer credential via Authorization header — an OAuth access token, or a long-lived user-issued AppKey (prefix bak_). No other headers required.",
		ToolCount:           len(tools),
		Tools:               tools,
		ClientConfigExample: cfg,
	}, nil
}
