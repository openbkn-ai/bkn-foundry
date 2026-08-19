package mcp

import (
	"context"
	"fmt"
	"net/http"

	oerrors "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
)

func (s *mcpServiceImpl) GetMCPInstanceConfig(ctx context.Context, mcpID string, mode interfaces.MCPMode) (*interfaces.MCPInstancConfigInfo, error) {
	// Get MCP Server release information.
	release, err := s.DBMCPServerRelease.SelectByMCPID(ctx, nil, mcpID)
	if err != nil {
		return nil, fmt.Errorf("select mcp server release failed: %w", err)
	}

	if release == nil {
		return nil, oerrors.NewHTTPError(ctx, http.StatusNotFound, oerrors.ErrExtMCPNotFound, "mcp server not exist")
	}

	// The custom MCP only acts as a proxy for external services. There are no instances on the platform side to serve, and the app endpoint cannot provide access.
	if release.CreationType == interfaces.MCPCreationTypeCustom.String() {
		return nil, oerrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.ErrExtMCPServerEndpointUnsupported,
			fmt.Sprintf("mcp server %s is a proxy to an external server, connect to its upstream url directly", mcpID))
	}

	// Verify execution permissions.
	accessor, err := s.AuthService.GetAccessor(ctx, "")
	if err != nil {
		return nil, err
	}
	err = s.AuthService.CheckExecutePermission(ctx, accessor, release.MCPID, interfaces.AuthResourceTypeMCP)
	if err != nil {
		return nil, err
	}

	// Assembly configuration information (the custom type has been returned in advance above, only the tool import type is left here)
	var config *interfaces.MCPInstancConfigInfo
	switch release.CreationType {
	case interfaces.MCPCreationTypeToolImported.String():
		config = &interfaces.MCPInstancConfigInfo{
			MCPID:   release.MCPID,
			Mode:    mode,
			URL:     "",
			Headers: utils.JSONToObject[map[string]string](release.Headers),
		}
	default:
		return nil, oerrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.ErrExtMCPNotFound, "mcp server not support this mode")
	}
	// Return configuration information.
	return config, nil
}
