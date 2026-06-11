package logic

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
)

type impexPayload map[string]any

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func cloneImpexForCreate(componentType string, raw json.RawMessage, newName string) (json.RawMessage, string, error) {
	var payload impexPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, "", fmt.Errorf("invalid impex json")
	}

	switch componentType {
	case "toolbox":
		return cloneToolboxImpexForCreate(payload, newName)
	case "mcp":
		return cloneMcpImpexForCreate(payload, newName)
	default:
		return raw, "", nil
	}
}

func cloneToolboxImpexForCreate(payload impexPayload, newName string) (json.RawMessage, string, error) {
	toolboxSection, ok := payload["toolbox"].(map[string]any)
	if !ok {
		return nil, "", fmt.Errorf("toolbox impex payload missing toolbox section")
	}
	configs, ok := toolboxSection["configs"].([]any)
	if !ok || len(configs) == 0 {
		return nil, "", fmt.Errorf("toolbox impex payload missing configs[0]")
	}
	item, ok := configs[0].(map[string]any)
	if !ok {
		return nil, "", fmt.Errorf("toolbox impex payload missing configs[0]")
	}

	newBoxID := newUUID()
	item["box_id"] = newBoxID
	if newName != "" {
		item["box_name"] = newName
	}
	item["status"] = "unpublish"

	operatorIDMap := map[string]string{}
	if operatorSection, ok := payload["operator"].(map[string]any); ok {
		if operatorConfigs, ok := operatorSection["configs"].([]any); ok {
			for _, cfg := range operatorConfigs {
				op, ok := cfg.(map[string]any)
				if !ok {
					continue
				}
				oldID := fmt.Sprint(op["operator_id"])
				newOpID := newUUID()
				metadataVersion := newUUID()
				if oldID != "" && oldID != "<nil>" {
					operatorIDMap[oldID] = newOpID
				}
				op["operator_id"] = newOpID
				if newName != "" {
					op["operator_name"] = newName
				}
				op["version"] = metadataVersion
				op["status"] = "unpublish"
				if metadata, ok := op["metadata"].(map[string]any); ok {
					metadata["version"] = metadataVersion
				}
			}
		}
	}

	if tools, ok := item["tools"].([]any); ok {
		for _, toolRaw := range tools {
			tool, ok := toolRaw.(map[string]any)
			if !ok {
				continue
			}
			tool["box_id"] = newBoxID
			tool["tool_id"] = newUUID()
			sourceType := fmt.Sprint(tool["source_type"])
			sourceID := fmt.Sprint(tool["source_id"])
			if sourceType == "operator" && sourceID != "" && sourceID != "<nil>" {
				if mapped, ok := operatorIDMap[sourceID]; ok {
					tool["source_id"] = mapped
				} else {
					tool["source_id"] = newUUID()
				}
			} else if sourceID != "" && sourceID != "<nil>" {
				tool["source_id"] = newUUID()
			}
		}
	}

	out, err := json.Marshal(payload)
	if err != nil {
		return nil, "", err
	}
	return out, newBoxID, nil
}

func cloneMcpImpexForCreate(payload impexPayload, newName string) (json.RawMessage, string, error) {
	mcpSection, ok := payload["mcp"].(map[string]any)
	if !ok {
		return nil, "", fmt.Errorf("mcp impex payload missing mcp section")
	}
	configs, ok := mcpSection["configs"].([]any)
	if !ok || len(configs) == 0 {
		return nil, "", fmt.Errorf("mcp impex payload missing configs[0]")
	}
	item, ok := configs[0].(map[string]any)
	if !ok {
		return nil, "", fmt.Errorf("mcp impex payload missing configs[0]")
	}

	boxIDMap := map[string]string{}
	toolIDMap := map[string]string{}

	if toolboxSection, ok := payload["toolbox"].(map[string]any); ok {
		if toolboxConfigs, ok := toolboxSection["configs"].([]any); ok {
			for _, cfg := range toolboxConfigs {
				toolbox, ok := cfg.(map[string]any)
				if !ok {
					continue
				}
				oldBoxID := fmt.Sprint(toolbox["box_id"])
				newBoxID := newUUID()
				if oldBoxID != "" && oldBoxID != "<nil>" {
					boxIDMap[oldBoxID] = newBoxID
				}
				toolbox["box_id"] = newBoxID
				toolbox["status"] = "unpublish"
				if tools, ok := toolbox["tools"].([]any); ok {
					for _, toolRaw := range tools {
						tool, ok := toolRaw.(map[string]any)
						if !ok {
							continue
						}
						oldToolID := fmt.Sprint(tool["tool_id"])
						newToolID := newUUID()
						if oldToolID != "" && oldToolID != "<nil>" {
							toolIDMap[oldToolID] = newToolID
						}
						tool["box_id"] = newBoxID
						tool["tool_id"] = newToolID
						if fmt.Sprint(tool["source_id"]) != "" {
							tool["source_id"] = newUUID()
						}
					}
				}
			}
		}
	}

	newMcpID := newUUID()
	item["mcp_id"] = newMcpID
	if newName != "" {
		item["name"] = newName
	}
	item["version"] = 1
	item["status"] = "unpublish"

	if mcpTools, ok := item["mcp_tools"].([]any); ok {
		for _, toolRaw := range mcpTools {
			tool, ok := toolRaw.(map[string]any)
			if !ok {
				continue
			}
			tool["mcp_id"] = newMcpID
			tool["mcp_tool_id"] = newUUID()
			tool["mcp_version"] = 1
			if mappedBoxID, ok := boxIDMap[fmt.Sprint(tool["box_id"])]; ok {
				tool["box_id"] = mappedBoxID
			}
			if mappedToolID, ok := toolIDMap[fmt.Sprint(tool["tool_id"])]; ok {
				tool["tool_id"] = mappedToolID
			}
		}
	}

	out, err := json.Marshal(payload)
	if err != nil {
		return nil, "", err
	}
	return out, newMcpID, nil
}

func impexNameFromExport(componentType string, raw json.RawMessage) string {
	var payload impexPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}

	switch componentType {
	case "toolbox":
		if toolboxSection, ok := payload["toolbox"].(map[string]any); ok {
			if configs, ok := toolboxSection["configs"].([]any); ok && len(configs) > 0 {
				if item, ok := configs[0].(map[string]any); ok {
					return fmt.Sprint(item["box_name"])
				}
			}
		}
	case "mcp":
		if mcpSection, ok := payload["mcp"].(map[string]any); ok {
			if configs, ok := mcpSection["configs"].([]any); ok && len(configs) > 0 {
				if item, ok := configs[0].(map[string]any); ok {
					return fmt.Sprint(item["name"])
				}
			}
		}
	}

	return ""
}
