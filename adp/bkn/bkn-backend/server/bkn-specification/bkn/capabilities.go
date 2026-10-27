// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Under 3.0 a malformed capabilities section is refused (SPECIFICATION.md §13). The 2.x reader maps
// the section onto BknCapabilities, which drops a misspelt category or field and the whole section
// on a type error, so a package imports with nothing mounted and nothing said. An entry that does
// not match in the target environment is still skipped at import, not refused here: whether it
// matches depends on the environment, not on how it is written.

// capabilityCategories are the categories of capabilities, in the order they are written.
var capabilityCategories = []string{"skills", "functions", "mcp_tools", "knowledge_bases"}

// capabilityFields are the fields of an entry of each category.
var capabilityFields = map[string][]string{
	"skills":          {"id", "name"},
	"functions":       {"box_id", "tool_id", "box_name", "tool_name"},
	"mcp_tools":       {"mcp_id", "mcp_name", "tool_name"},
	"knowledge_bases": {"id", "name"},
}

// parseCapabilitiesV3 reads the capabilities section of a 3.0 network.bkn. Values are read as
// written. A section with no entries is nil, as under 2.x.
func parseCapabilitiesV3(node *yaml.Node) (*BknCapabilities, error) {
	if node == nil {
		return nil, nil
	}
	if node.Kind != yaml.MappingNode {
		return nil, specErrorf("capabilities must be a mapping with %s", strings.Join(capabilityCategories, ", "))
	}
	capabilities := &BknCapabilities{}
	for i := 0; i+1 < len(node.Content); i += 2 {
		category, list := node.Content[i].Value, node.Content[i+1]
		fields, known := capabilityFields[category]
		if !known {
			return nil, specErrorf("capabilities category %q is unknown; the categories are %s", category, strings.Join(capabilityCategories, ", "))
		}
		if list.Kind != yaml.SequenceNode {
			return nil, specErrorf("capabilities %s must be a list of entries", category)
		}
		for n, item := range list.Content {
			where := fmt.Sprintf("capabilities %s item %d", category, n+1)
			entry, err := readCapabilityEntry(where, item, fields)
			if err != nil {
				return nil, err
			}
			if err := addCapabilityEntry(capabilities, where, category, entry); err != nil {
				return nil, err
			}
		}
	}
	if capabilities.IsEmpty() {
		return nil, nil
	}
	return capabilities, nil
}

// readCapabilityEntry reads one entry: a mapping of the category's fields to single values.
func readCapabilityEntry(where string, item *yaml.Node, fields []string) (map[string]string, error) {
	if item.Kind != yaml.MappingNode {
		return nil, specErrorf("%s must be a mapping of %s", where, strings.Join(fields, ", "))
	}
	entry := map[string]string{}
	for i := 0; i+1 < len(item.Content); i += 2 {
		key, value := item.Content[i].Value, item.Content[i+1]
		if !containsString(fields, key) {
			return nil, specErrorf("%s field %q is unknown; the fields are %s", where, key, strings.Join(fields, ", "))
		}
		if value.Kind != yaml.ScalarNode || value.Tag == "!!null" {
			return nil, specErrorf("%s %s must be a single value", where, key)
		}
		entry[key] = value.Value
	}
	return entry, nil
}

// addCapabilityEntry adds an entry to its category, refusing one that lacks what the import
// matches on and so matches in no environment.
func addCapabilityEntry(capabilities *BknCapabilities, where, category string, e map[string]string) error {
	switch category {
	case "skills":
		if e["id"] == "" && e["name"] == "" {
			return specErrorf("%s has neither id nor name, so it matches no skill", where)
		}
		capabilities.Skills = append(capabilities.Skills, &BknCapabilitySkill{ID: e["id"], Name: e["name"]})
	case "functions":
		if (e["box_id"] == "" || e["tool_id"] == "") && (e["box_name"] == "" || e["tool_name"] == "") {
			return specErrorf("%s needs box_id and tool_id, or box_name and tool_name, to match a tool", where)
		}
		capabilities.Functions = append(capabilities.Functions, &BknCapabilityFunction{
			BoxID: e["box_id"], ToolID: e["tool_id"], BoxName: e["box_name"], ToolName: e["tool_name"],
		})
	case "mcp_tools":
		if e["tool_name"] == "" {
			return specErrorf("%s needs tool_name: an MCP tool is addressed by its name", where)
		}
		if e["mcp_id"] == "" && e["mcp_name"] == "" {
			return specErrorf("%s has neither mcp_id nor mcp_name, so it matches no MCP server", where)
		}
		capabilities.MCPTools = append(capabilities.MCPTools, &BknCapabilityMCPTool{
			MCPID: e["mcp_id"], MCPName: e["mcp_name"], ToolName: e["tool_name"],
		})
	case "knowledge_bases":
		if e["id"] == "" && e["name"] == "" {
			return specErrorf("%s has neither id nor name, so it matches no knowledge base", where)
		}
		capabilities.KnowledgeBases = append(capabilities.KnowledgeBases, &BknCapabilityKnowledgeBase{ID: e["id"], Name: e["name"]})
	}
	return nil
}
