package deployer

import (
	"context"
	"fmt"
	"sync"

	"github.com/mark3labs/mcp-go/server"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

var (
	httpDeployerOnce sync.Once
	httpDeployer     Deployer
)

// streamableHTTPDeployer Streamable HTTP deployer.
type streamableHTTPDeployer struct{}

// newStreamableHTTPDeployer creates a streamable HTTP deployer.
func newStreamableHTTPDeployer() Deployer {
	httpDeployerOnce.Do(func() {
		httpDeployer = &streamableHTTPDeployer{}
	})
	return httpDeployer
}

// Deploy deploy MCP instance.
func (d *streamableHTTPDeployer) Deploy(ctx context.Context, instance *interfaces.MCPServerInstance) error {
	streamPath := fmt.Sprintf("/app/%s/%d/stream", instance.Config.MCPID, instance.Config.Version)
	streamServer := server.NewStreamableHTTPServer(instance.MCPServer, server.WithEndpointPath(streamPath))
	instance.StreamServer = streamServer
	instance.StreamRoutePath = streamPath
	return nil
}

// Undeploy Uninstall.
func (d *streamableHTTPDeployer) Undeploy(ctx context.Context, instance *interfaces.MCPServerInstance) error {
	if instance.StreamServer != nil {
		return instance.StreamServer.Shutdown(ctx)
	}
	return nil
}
