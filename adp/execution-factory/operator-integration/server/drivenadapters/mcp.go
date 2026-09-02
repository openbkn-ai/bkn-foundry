package drivenadapters

import (
	"context"
	"crypto/tls"
	stderrors "errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
)

const (
	mcpClientName      = "agent-operator-integration"
	mcpClientVersion   = "1.0.0"
	defaultConnTimeout = 30 * time.Second
)

// MCPClient defines MCP client.
type MCPClient struct {
	MCPCoreConfigInfo *interfaces.MCPCoreConfigInfo
	client            *client.Client
	serverInitInfo    *mcp.InitializeResult
}

// NewMCPClient creates MCP client.
func NewMCPClient(ctx context.Context, mcpCoreInfo *interfaces.MCPCoreConfigInfo) (interfaces.MCPClient, error) {
	safeCoreInfo := *mcpCoreInfo
	safeCoreInfo.Headers = utils.SanitizeThirdPartyHeaders(mcpCoreInfo.Headers)
	mcpClient := &MCPClient{
		MCPCoreConfigInfo: &safeCoreInfo,
	}
	if err := mcpClient.initClient(ctx); err != nil {
		return nil, classifyMCPConnError(ctx, mcpCoreInfo.URL, err)
	}
	return mcpClient, nil
}

// mcpUpstreamStatusPatterns is used to extract the HTTP status codes returned by upstream from the error text of mcp-go.
// mcp-go v0.37.0 does not export the error type carrying the status code: SSE transmission returns during the connection establishment phase.
// "unexpected status code: 401", streamable HTTP transport returned during initialization phase.
// "request failed with status 401: <body>" can only be extracted by text.
var mcpUpstreamStatusPatterns = []*regexp.Regexp{
	regexp.MustCompile(`unexpected status code: (\d{3})`),
	regexp.MustCompile(`request failed with status (\d{3})`),
}

// upstreamHTTPStatus extracts the HTTP status code returned by the upstream, and returns 0 if it cannot be extracted.
func upstreamHTTPStatus(err error) int {
	if err == nil {
		return 0
	}
	msg := err.Error()
	for _, pattern := range mcpUpstreamStatusPatterns {
		matches := pattern.FindStringSubmatch(msg)
		if len(matches) != 2 {
			continue
		}
		if status, convErr := strconv.Atoi(matches[1]); convErr == nil {
			return status
		}
	}
	return 0
}

// classifyMCPConnError distinguishes between "credential issues" and "service unreachable":
// The upstream returns 401/403/407, indicating that the MCP service itself is normal, but the credentials in the request header are missing, expired, or have insufficient permissions.
// Prompting users to check whether the service is running can lead people in the wrong direction.
func classifyMCPConnError(ctx context.Context, url string, err error) error {
	// When initClient has given a clear error (such as the mode is not supported), it is transparently transmitted as it is to avoid being folded into unreachable.
	var httpErr *errors.HTTPError
	if stderrors.As(err, &httpErr) {
		return httpErr
	}

	switch status := upstreamHTTPStatus(err); status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusProxyAuthRequired:
		return errors.NewHTTPError(ctx, http.StatusBadRequest,
			errors.ErrExtMCPServerAuthFailed,
			fmt.Sprintf("mcp server %s rejected the request with status %d, please check the credentials in request headers, error: %v", url, status, err),
			status)
	}

	return errors.NewHTTPError(ctx, http.StatusServiceUnavailable,
		errors.ErrExtMCPServerNotAccessible,
		fmt.Sprintf("mcp server %s is not accessible, please check if the MCP server is running, error: %v", url, err))
}

// NewInProcessMCPClient creates an in-process MCP client.
func NewInProcessMCPClient(ctx context.Context, server *server.MCPServer) (interfaces.MCPClient, error) {
	mcpClient := &MCPClient{
		MCPCoreConfigInfo: &interfaces.MCPCoreConfigInfo{
			Mode: interfaces.MCPModeStream, // In-process behaves like a stream
			URL:  "in-process",
		},
	}
	cli, err := client.NewInProcessClient(server)
	if err != nil {
		return nil, err
	}
	mcpClient.client = cli

	if err := mcpClient.performHandshake(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize in-process client: %w", err)
	}

	return mcpClient, nil
}

// ListTools list tools.
func (m *MCPClient) ListTools(ctx context.Context, req mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
	serverCapabilities := m.client.GetServerCapabilities()
	if serverCapabilities.Tools == nil {
		return &mcp.ListToolsResult{
			Tools: []mcp.Tool{},
		}, nil
	}

	result, err := m.client.ListTools(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("[mcpClient.ListTools] failed to list tools:\n %v", err)
	}
	return result, nil
}

// CallTool call tool.
func (m *MCPClient) CallTool(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	result, err := m.client.CallTool(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to call tool: %w", err)
	}
	return result, nil
}

func (m *MCPClient) initClient(ctx context.Context) error {
	mode := m.MCPCoreConfigInfo.Mode
	var cli *client.Client
	var err error

	switch mode {
	case interfaces.MCPModeSSE:
		httpClient := m.createHTTPClient()
		cli, err = client.NewSSEMCPClient(m.MCPCoreConfigInfo.URL, client.WithHeaders(m.MCPCoreConfigInfo.Headers), client.WithHTTPClient(httpClient))
	case interfaces.MCPModeStream:
		httpClient := m.createHTTPClient()
		cli, err = client.NewStreamableHttpClient(m.MCPCoreConfigInfo.URL, transport.WithHTTPHeaders(m.MCPCoreConfigInfo.Headers), transport.WithHTTPBasicClient(httpClient))
	default:
		return errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtMCPModeNotSupported,
			fmt.Sprintf("MCP mode %s is not supported", mode), mode)
	}

	if err != nil {
		err = fmt.Errorf("[mcp.initClient] failed to create %s MCP client:\n %v", mode, err)
		return err
	}

	m.client = cli
	return m.performHandshake(ctx)
}

var (
	httpClient     *http.Client
	httpClientOnce sync.Once
)

func (m *MCPClient) createHTTPClient() *http.Client {
	httpClientOnce.Do(func() {
		connTimeout := time.Duration(config.NewConfigLoader().MCPConfig.ConnTimeout) * time.Second
		if connTimeout <= 0 {
			connTimeout = defaultConnTimeout
		}
		httpClient = &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
				DialContext: (&net.Dialer{
					Timeout:   connTimeout,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				MaxIdleConns:        200,
				MaxIdleConnsPerHost: 50,
				IdleConnTimeout:     90 * time.Second,
			},
		}
	})
	return httpClient
}

// performHandshake performs MCP handshake.
func (m *MCPClient) performHandshake(ctx context.Context) error {
	// 1. Start connection
	if err := m.client.Start(ctx); err != nil {
		return fmt.Errorf("failed to start MCP client: %w", err)
	}

	// 2. Initialize
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.Capabilities = mcp.ClientCapabilities{}
	initReq.Params.ClientInfo = mcp.Implementation{
		Name:    mcpClientName,
		Version: mcpClientVersion,
	}

	initResp, err := m.client.Initialize(ctx, initReq)
	if err != nil {
		return fmt.Errorf("failed to initialize MCP client: %w", err)
	}
	m.serverInitInfo = initResp
	// 3. Ping
	if err := m.client.Ping(ctx); err != nil {
		return fmt.Errorf("failed to ping MCP client: %w", err)
	}
	return nil
}

// GetInitInfo gets initialization information.
func (m *MCPClient) GetInitInfo(ctx context.Context) *mcp.InitializeResult {
	return m.serverInitInfo
}

func (m *MCPClient) Close() error {
	if m.client != nil {
		return m.client.Close()
	}
	return nil
}
