package utils

import "fmt"

// GenerateMCPServerVersion generates MCP Server version.
func GenerateMCPServerVersion(mcpVersion int) string {
	return fmt.Sprintf("%d.0.0", mcpVersion)
}
