package mcp

import (
	"context"
	"fmt"
	"net/http"

	oerrors "github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/utils"
)

func (s *mcpServiceImpl) GetMCPInstanceConfig(ctx context.Context, mcpID string, mode interfaces.MCPMode) (*interfaces.MCPInstancConfigInfo, error) {
	// 获取MCP Server发布信息
	release, err := s.DBMCPServerRelease.SelectByMCPID(ctx, nil, mcpID)
	if err != nil {
		return nil, fmt.Errorf("select mcp server release failed: %w", err)
	}

	if release == nil {
		return nil, oerrors.NewHTTPError(ctx, http.StatusNotFound, oerrors.ErrExtMCPNotFound, "mcp server not exist")
	}

	// 自定义型MCP只是代理外部服务，平台侧没有实例可服务，app endpoint 无法提供接入
	if release.CreationType == interfaces.MCPCreationTypeCustom.String() {
		return nil, oerrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.ErrExtMCPServerEndpointUnsupported,
			fmt.Sprintf("mcp server %s is a proxy to an external server, connect to its upstream url directly", mcpID))
	}

	// 校验执行权限
	accessor, err := s.AuthService.GetAccessor(ctx, "")
	if err != nil {
		return nil, err
	}
	err = s.AuthService.CheckExecutePermission(ctx, accessor, release.MCPID, interfaces.AuthResourceTypeMCP)
	if err != nil {
		return nil, err
	}

	// 组装配置信息（custom 型已在上面提前返回，这里只剩工具导入型）
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
	// 返回配置信息
	return config, nil
}
