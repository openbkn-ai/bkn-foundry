package logic

import (
	"fmt"
	"strings"
)

const (
	HttpCapabilityPrefix     = "http:"
	FunctionCapabilityPrefix = "function:"
	McpCapabilityPrefix      = "mcp:"
	SkillCapabilityPrefix    = "skill:"
)

func BuildHttpCapabilityID(boxID, toolID string) string {
	return fmt.Sprintf("%s%s:%s", HttpCapabilityPrefix, boxID, toolID)
}

func BuildFunctionCapabilityID(boxID, toolID string) string {
	return fmt.Sprintf("%s%s:%s", FunctionCapabilityPrefix, boxID, toolID)
}

func BuildMcpCapabilityID(mcpID string) string {
	return McpCapabilityPrefix + mcpID
}

func BuildSkillCapabilityID(skillID string) string {
	return SkillCapabilityPrefix + skillID
}

func ParseFunctionCapabilityID(id string) (boxID, toolID string, ok bool) {
	if !strings.HasPrefix(id, FunctionCapabilityPrefix) {
		return "", "", false
	}

	rest := strings.TrimPrefix(id, FunctionCapabilityPrefix)
	parts := strings.SplitN(rest, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}

	return parts[0], parts[1], true
}

func ParseHttpCapabilityID(id string) (boxID, toolID string, ok bool) {
	if !strings.HasPrefix(id, HttpCapabilityPrefix) {
		return "", "", false
	}

	rest := strings.TrimPrefix(id, HttpCapabilityPrefix)
	parts := strings.SplitN(rest, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}

	return parts[0], parts[1], true
}

func ParseMcpCapabilityID(id string) (mcpID string, ok bool) {
	if !strings.HasPrefix(id, McpCapabilityPrefix) {
		return "", false
	}

	mcpID = strings.TrimPrefix(id, McpCapabilityPrefix)
	return mcpID, mcpID != ""
}

func ParseSkillCapabilityID(id string) (skillID string, ok bool) {
	if !strings.HasPrefix(id, SkillCapabilityPrefix) {
		return "", false
	}

	skillID = strings.TrimPrefix(id, SkillCapabilityPrefix)
	return skillID, skillID != ""
}

func ParseCapabilityKind(id string) string {
	switch {
	case strings.HasPrefix(id, HttpCapabilityPrefix):
		return "http"
	case strings.HasPrefix(id, FunctionCapabilityPrefix):
		return "function"
	case strings.HasPrefix(id, McpCapabilityPrefix):
		return "mcp"
	case strings.HasPrefix(id, SkillCapabilityPrefix):
		return "skill"
	default:
		return ""
	}
}
