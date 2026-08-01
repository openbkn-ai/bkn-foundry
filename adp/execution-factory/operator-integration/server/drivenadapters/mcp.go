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

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	mcpClientName      = "agent-operator-integration"
	mcpClientVersion   = "1.0.0"
	defaultConnTimeout = 30 * time.Second
)

// MCPClient 定义 MCP 客户端
type MCPClient struct {
	MCPCoreConfigInfo *interfaces.MCPCoreConfigInfo
	client            *client.Client
	serverInitInfo    *mcp.InitializeResult
}

// NewMCPClient 创建 MCP 客户端
func NewMCPClient(ctx context.Context, mcpCoreInfo *interfaces.MCPCoreConfigInfo) (interfaces.MCPClient, error) {
	mcpClient := &MCPClient{
		MCPCoreConfigInfo: mcpCoreInfo,
	}
	if err := mcpClient.initClient(ctx); err != nil {
		return nil, classifyMCPConnError(ctx, mcpCoreInfo.URL, err)
	}
	return mcpClient, nil
}

// mcpUpstreamStatusPatterns 用于从 mcp-go 的错误文本中提取上游返回的 HTTP 状态码。
// mcp-go v0.37.0 未导出携带状态码的错误类型：SSE 传输在建连阶段返回
// "unexpected status code: 401"，streamable HTTP 传输在初始化阶段返回
// "request failed with status 401: <body>"，只能按文本提取。
var mcpUpstreamStatusPatterns = []*regexp.Regexp{
	regexp.MustCompile(`unexpected status code: (\d{3})`),
	regexp.MustCompile(`request failed with status (\d{3})`),
}

// upstreamHTTPStatus 提取上游返回的 HTTP 状态码，提取不到返回 0
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

// classifyMCPConnError 区分「凭据问题」与「服务不可达」：
// 上游返回 401/403/407 说明 MCP 服务本身是通的，只是请求头里的凭据缺失、过期或权限不足，
// 提示用户去查服务是否运行会把人引到错误方向。
func classifyMCPConnError(ctx context.Context, url string, err error) error {
	// initClient 已经给出明确错误（如模式不支持）时原样透传，避免被折叠成不可达
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

// NewInProcessMCPClient 创建进程内 MCP 客户端
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

// ListTools 列出工具
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

// CallTool 调用工具
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

// performHandshake 执行 MCP 握手
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

// GetInitInfo 获取初始化信息
func (m *MCPClient) GetInitInfo(ctx context.Context) *mcp.InitializeResult {
	return m.serverInitInfo
}

func (m *MCPClient) Close() error {
	if m.client != nil {
		return m.client.Close()
	}
	return nil
}
