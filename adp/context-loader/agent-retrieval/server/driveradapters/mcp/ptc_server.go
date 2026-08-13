// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// PTC（代码化工具调用）的独立 MCP 端点。
//
// 与 /mcp 的差别只在工具面：那边是二十来个业务工具逐个暴露，这边只有 run_code /
// run_shell 加会话生命周期。两者不能并存于同一个端点——客户端同时看到 run_code
// 和二十个业务工具时，模型会挑后者，PTC 就退化成了普通工具调用。
//
// 执行发生在服务端：客户端只发一段代码，context-loader 拿**调用方本人的令牌**去打
// 执行工厂的公开面。这样任何 MCP 客户端（Claude Desktop、Cursor、第三方 Agent）
// 都能用 PTC，不必自己实现「取工具包 + 拼 handler + 打执行工厂」那一整套。
const (
	ptcEndpointPath   = "/api/agent-retrieval/v1/mcp/ptc"
	ptcServerName     = "context-loader-ptc"
	ptcDefaultTimeout = 60
	// defaultPTCServicePort 是沙箱回访本服务时用的集群内端口，可由
	// PTC_SANDBOX_MCP_URL / PTC_SANDBOX_MCP_HOST 覆盖（见 sandboxMCPURL）。
	defaultPTCServicePort = 30779
	// ptcMaxTimeout 与执行工厂的沙箱超时同量级；再长的任务不该占着一次工具调用。
	ptcMaxTimeout = 600
)

// NewPTCMCPHandler 创建 PTC MCP 端点的 http.Handler。
func NewPTCMCPHandler() (http.Handler, error) {
	return NewPTCMCPHandlerWith(
		bkntrace.NewLifecycleClientFromEnv(),
		drivenadapters.NewOperatorIntegrationClient(),
		defaultPTCServicePort,
	)
}

// NewPTCMCPHandlerWith 注入依赖创建（测试用）。
func NewPTCMCPHandlerWith(
	lifecycleClient *bkntrace.LifecycleClient,
	executor interfaces.DrivenOperatorIntegration,
	servicePort int,
) (http.Handler, error) {
	srv, err := newPTCMCPServer(lifecycleClient, executor, servicePort)
	if err != nil {
		return nil, err
	}
	return server.NewStreamableHTTPServer(srv,
		server.WithHTTPContextFunc(func(ctx context.Context, r *http.Request) context.Context {
			return common.CopyRequestScopedValues(r.Context(), ctx)
		}),
		server.WithEndpointPath(ptcEndpointPath),
	), nil
}

func newPTCMCPServer(
	lifecycleClient *bkntrace.LifecycleClient,
	executor interfaces.DrivenOperatorIntegration,
	servicePort int,
) (*server.MCPServer, error) {
	// 工具包在装配期渲染一次：内容只取决于工具目录与环境变量，与请求无关。
	toolkit, err := BuildPTCToolkit(ptcEndpointPath, servicePort)
	if err != nil {
		return nil, err
	}
	localeBundle := loadMCPLocaleBundle(mcpLocaleFromEnv())

	mcpServer := server.NewMCPServer(ptcServerName, serverVersion,
		server.WithToolCapabilities(true),
		server.WithInstructions(ptcServerInstructions),
		// run_code / run_shell 是业务工具，要进证据链：这个中间件对非生命周期
		// 工具强制 bkn_context，并把每次调用登记成一个 operation。
		server.WithToolHandlerMiddleware(lifecycleToolMiddleware(lifecycleClient)),
	)
	// 生命周期工具必须一起暴露：外部客户端没有 studio 那样的前端帮它开关交互，
	// 不给它 bkn_start_interaction，它连第一次 run_code 都发不出去。
	registerLifecycleTools(mcpServer, lifecycleClient, localeBundle)

	for _, tool := range toolkit.Tools {
		input := ptcToolInputSchemaWithContext(tool.InputSchema)
		mcpServer.AddTool(
			newToolWithSchemas(ToolMeta{Name: tool.Name, Description: tool.Description, Title: tool.Name}, input, ptcOutputSchema),
			handlePTCExecute(executor, toolkit, tool),
		)
	}
	return mcpServer, nil
}

const ptcServerInstructions = `本端点只提供两个执行工具：run_code（Python）与 run_shell（shell）。

BKN 的全部能力——知识网络、对象类、实例查询、SQL——都在 run_code 的沙箱里以
函数形式提供，签名清单见 run_code 的工具描述。请把整个问题写成一段脚本，在脚本内
完成探查、取数与聚合，只 print 结论；不要一次调用只做一件事。

调用前先 bkn_start_interaction 拿到 conversation_id 与 interaction_id，随后每次
调用都带上 bkn_context，结束时 bkn_finish_interaction。`

// ptcOutputSchema run_code / run_shell 的返回结构。
var ptcOutputSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "stdout": {"type": "string", "description": "标准输出。只有 print 的内容会回到这里。"},
    "stderr": {"type": "string", "description": "标准错误。退出码为 0 时也可能非空（库告警等）。"},
    "exit_code": {"type": "integer", "description": "退出码。非 0 表示脚本自身失败，报文在 stderr 里。"}
  },
  "required": ["stdout", "exit_code"]
}`)

// ptcToolInputSchemaWithContext 给工具入参补上 bkn_context。
//
// 工具包里的 schema 是给 studio 用的——那边由前端管理会话，bkn_context 不该出现在
// 模型的入参里。MCP 客户端没有那一层，必须自己带，所以在这里补进去。
func ptcToolInputSchemaWithContext(raw json.RawMessage) json.RawMessage {
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return raw
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		return raw
	}
	properties["bkn_context"] = map[string]any{
		"type":        "object",
		"description": "会话上下文，取自 bkn_start_interaction 的返回。",
		"properties": map[string]any{
			"conversation_id": map[string]any{"type": "string"},
			"interaction_id":  map[string]any{"type": "string"},
		},
		"required": []any{"conversation_id", "interaction_id"},
	}
	if required, ok := schema["required"].([]any); ok {
		schema["required"] = append(required, "bkn_context")
	} else {
		schema["required"] = []any{"bkn_context"}
	}
	encoded, err := json.Marshal(schema)
	if err != nil {
		return raw
	}
	return encoded
}

func handlePTCExecute(
	executor interfaces.DrivenOperatorIntegration, toolkit *PTCToolkit, tool PTCTool,
) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		timeout := int(req.GetFloat("timeout", ptcDefaultTimeout))
		if timeout <= 0 {
			timeout = ptcDefaultTimeout
		}
		if timeout > ptcMaxTimeout {
			timeout = ptcMaxTimeout
		}
		businessContext := ptcBusinessContextArg(req)

		code, apiErr := buildPTCCode(toolkit, tool, req, businessContext)
		if apiErr != "" {
			return mcp.NewToolResultError(apiErr), nil
		}

		event := map[string]any{"bkn": businessContext}
		if tool.Wrap == ptcWrapHandler {
			// 只有 Python 那条路会回访 MCP，需要端点与凭据；shell 不需要，也就
			// 不下发——沙箱里少一份可读到的令牌。
			token, _ := common.GetRawTokenFromCtx(ctx)
			event["mcp"] = toolkit.SandboxMCPURL
			event["token"] = token
		}

		resp, err := executor.ExecuteFunction(ctx, &interfaces.ExecuteFunctionRequest{
			Code: code, Language: tool.Language, Event: event, Timeout: timeout,
		})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		payload := map[string]any{
			"stdout": resp.Stdout, "stderr": resp.Stderr, "exit_code": resp.ExitCode,
		}
		// 退出码非 0 标成工具错误，让调用方的模型据 stderr 自行修正；报文照常带回，
		// 吞掉它等于让对方盲目重试。
		result, err := BuildMCPToolResult(payload, rest.FormatJSON)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if resp.ExitCode != 0 {
			result.IsError = true
		}
		return result, nil
	}
}

// buildPTCCode 把模型给的入参组装成交给沙箱的完整脚本。
// 两种组装方式与 PTCTool.Wrap 的取值一一对应，客户端侧走的是同一套规则。
func buildPTCCode(
	toolkit *PTCToolkit, tool PTCTool, req mcp.CallToolRequest, businessContext map[string]any,
) (string, string) {
	switch tool.Wrap {
	case ptcWrapHandler:
		code := strings.TrimRight(getStringArg(req, "code", ""), "\n")
		if strings.TrimSpace(code) == "" {
			return "", "code is required"
		}
		var body strings.Builder
		for _, line := range strings.Split(code, "\n") {
			if strings.TrimSpace(line) == "" {
				body.WriteString("\n")
				continue
			}
			body.WriteString("    " + line + "\n")
		}
		// 沙箱按 Lambda 规范执行，入口必须是单参数的 handler(event)。
		return fmt.Sprintf("%s\n\ndef handler(event):\n    _configure(event)\n%s", toolkit.Stub, body.String()), ""
	case ptcWrapCdWorkdir:
		command := strings.TrimSpace(getStringArg(req, "command", ""))
		if command == "" {
			return "", "command is required"
		}
		// 与 run_code 落在同一个目录，两个工具看到同一批文件。目录名只含
		// [A-Za-z0-9_-]，可以安全地拼进命令。
		workdir := ptcWorkdir(businessContext)
		return fmt.Sprintf("mkdir -p %s && cd %s\n%s", workdir, workdir, command), ""
	default:
		return "", "unsupported tool wrap: " + tool.Wrap
	}
}

// ptcWorkdir 本次对话在沙箱里的工作目录。
//
// 规则必须与 stub 里 _configure 的实现逐字一致（归一化字符集、截断长度、缺省名），
// 否则 run_shell 与 run_code 会落在两个目录里，彼此看不见对方写的文件。
func ptcWorkdir(businessContext map[string]any) string {
	conversation, _ := businessContext["conversation_id"].(string)
	var safe strings.Builder
	for _, r := range strings.TrimSpace(conversation) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			safe.WriteRune(r)
		default:
			safe.WriteRune('-')
		}
		if safe.Len() >= 64 {
			break
		}
	}
	if safe.Len() == 0 {
		return "/workspace/shared"
	}
	return "/workspace/conv-" + safe.String()
}

// ptcBusinessContextArg 取出 bkn_context。
//
// 生命周期中间件已经校验过它在场且字段合法，这里只负责取值转发给沙箱——沙箱侧的
// stub 用它挑工作目录，并在回访 MCP 时原样带上，好让脚本内的调用挂在同一次交互下。
func ptcBusinessContextArg(req mcp.CallToolRequest) map[string]any {
	raw, ok := req.GetRawArguments().(map[string]any)
	if !ok {
		return map[string]any{}
	}
	value, ok := raw["bkn_context"].(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return value
}
