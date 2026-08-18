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

// PTC is the dedicated MCP endpoint for programmatic tool calling.
//
// It exposes only run_code, run_shell, and the lifecycle tools. That is a
// narrower surface than /mcp, which since registerInlinePTCTools carries the
// execution tools alongside the business tools.
//
// This endpoint predates that and was originally justified by the belief that
// the two surfaces must not coexist, because a model given both would pick the
// individual tools and bypass PTC. Measurement disproved it — see the comment
// on registerInlinePTCTools — so what it is good for now is a client that
// deliberately wants code mode only: a small fixed tool list regardless of how
// many business tools this service grows, and a run_code description that
// carries the full Python signatures, since here there is no other tool on the
// surface to read a schema from.
//
// Execution occurs server-side with the caller's token, allowing any MCP client
// to use PTC without implementing toolkit retrieval and execution-factory calls.
const (
	ptcEndpointPath   = "/api/agent-retrieval/v1/mcp/ptc"
	ptcServerName     = "context-loader-ptc"
	ptcDefaultTimeout = 60
	// defaultPTCServicePort is the in-cluster port used when the sandbox calls
	// back. PTC_SANDBOX_MCP_URL and PTC_SANDBOX_MCP_HOST override it.
	defaultPTCServicePort = 30779
	// ptcMaxTimeout aligns with the execution-factory sandbox timeout.
	ptcMaxTimeout = 600
)

// NewPTCMCPHandler creates the PTC MCP HTTP handler.
func NewPTCMCPHandler() (http.Handler, error) {
	return NewPTCMCPHandlerWith(
		bkntrace.NewLifecycleClientFromEnv(),
		drivenadapters.NewOperatorIntegrationClient(),
		defaultPTCServicePort,
	)
}

// NewPTCMCPHandlerWith creates the handler with injected dependencies for tests.
func NewPTCMCPHandlerWith(
	lifecycleClient *bkntrace.LifecycleClient,
	executor interfaces.DrivenOperatorIntegration,
	servicePort int,
) (http.Handler, error) {
	handlers := make(map[string]http.Handler, 2)
	for _, locale := range []string{"zh-CN", "en-US"} {
		srv, err := newPTCMCPServerForLocale(lifecycleClient, executor, servicePort, locale)
		if err != nil {
			return nil, err
		}
		handlers[locale] = newMCPStreamableHTTPHandler(srv, ptcEndpointPath)
	}
	return &localizedMCPHandler{handlers: handlers}, nil
}

func newPTCMCPServer(
	lifecycleClient *bkntrace.LifecycleClient,
	executor interfaces.DrivenOperatorIntegration,
	servicePort int,
) (*server.MCPServer, error) {
	return newPTCMCPServerForLocale(lifecycleClient, executor, servicePort, defaultMCPLocale)
}

func newPTCMCPServerForLocale(
	lifecycleClient *bkntrace.LifecycleClient,
	executor interfaces.DrivenOperatorIntegration,
	servicePort int,
	locale string,
) (*server.MCPServer, error) {
	// Render the toolkit during assembly because it depends only on the tool
	// catalog and environment, not an individual request.
	toolkit, err := BuildPTCToolkitForLocale(ptcEndpointPath, servicePort, locale)
	if err != nil {
		return nil, err
	}
	localeBundle := loadMCPLocaleBundle(locale)

	mcpServer := server.NewMCPServer(ptcServerName, serverVersion,
		server.WithToolCapabilities(true),
		server.WithInstructions(localeBundle.PTCServerInstructions()),
		// run_code and run_shell are business tools. The middleware requires
		// bkn_context for non-lifecycle tools and records each call as an operation.
		server.WithToolHandlerMiddleware(lifecycleToolMiddleware(lifecycleClient)),
	)
	// External clients require lifecycle tools to start and finish interactions.
	registerLifecycleTools(mcpServer, lifecycleClient, localeBundle)

	for _, tool := range toolkit.Tools {
		input := ptcToolInputSchemaWithContext(tool.InputSchema, localeBundle.PTCResource("ptc_bkn_context_description.txt"))
		mcpServer.AddTool(
			newToolWithSchemas(ToolMeta{Name: tool.Name, Description: tool.Description, Title: tool.Name}, input,
				json.RawMessage(localeBundle.PTCResource("ptc_output_schema.json"))),
			handlePTCExecuteForLocale(executor, toolkit, tool, localeBundle),
		)
	}
	return mcpServer, nil
}

// ptcToolInputSchemaWithContext adds bkn_context to a tool's input schema.
//
// The toolkit schema is used by Studio, where the frontend manages the session.
// MCP clients must provide bkn_context themselves, so it is added here.
func ptcToolInputSchemaWithContext(raw json.RawMessage, contextDescription string) json.RawMessage {
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
		"description": contextDescription,
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
	return handlePTCExecuteForLocale(executor, toolkit, tool, loadMCPLocaleBundle(defaultMCPLocale))
}

func handlePTCExecuteForLocale(
	executor interfaces.DrivenOperatorIntegration, toolkit *PTCToolkit, tool PTCTool, locale *mcpLocaleBundle,
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

		code, apiErr := buildPTCCode(locale, toolkit, tool, req, businessContext)
		if apiErr != "" {
			return mcp.NewToolResultError(apiErr), nil
		}

		event := map[string]any{"bkn": businessContext}
		if tool.Wrap == ptcWrapHandler {
			// Only Python calls back to MCP and requires the endpoint and token.
			token, _ := common.GetRawTokenFromCtx(ctx)
			event["mcp"] = toolkit.SandboxMCPURL
			event["token"] = token
			event["locale"] = common.GetLanguageFromCtx(ctx)
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
		// A non-zero exit code is a tool error. Return stderr so the caller can
		// correct its script instead of blindly retrying.
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

// buildPTCCode turns model input into the full script sent to the sandbox.
// Its assembly modes correspond one-to-one with PTCTool.Wrap values.
func buildPTCCode(
	locale *mcpLocaleBundle, toolkit *PTCToolkit, tool PTCTool, req mcp.CallToolRequest, businessContext map[string]any,
) (string, string) {
	switch tool.Wrap {
	case ptcWrapHandler:
		code := strings.TrimRight(getStringArg(req, "code", ""), "\n")
		if strings.TrimSpace(code) == "" {
			return "", locale.PTCError("code_required")
		}
		var body strings.Builder
		for _, line := range strings.Split(code, "\n") {
			if strings.TrimSpace(line) == "" {
				body.WriteString("\n")
				continue
			}
			body.WriteString("    " + line + "\n")
		}
		// The sandbox follows the Lambda convention: handler(event).
		return fmt.Sprintf("%s\n\ndef handler(event):\n    _configure(event)\n%s", toolkit.Stub, body.String()), ""
	case ptcWrapCdWorkdir:
		command := strings.TrimSpace(getStringArg(req, "command", ""))
		if command == "" {
			return "", locale.PTCError("command_required")
		}
		// run_shell shares the run_code work directory. The directory name only
		// contains [A-Za-z0-9_-], so it can be embedded safely in the command.
		workdir := ptcWorkdir(businessContext)
		return fmt.Sprintf("mkdir -p %s && cd %s\n%s", workdir, workdir, command), ""
	default:
		return "", locale.PTCError("unsupported_tool_wrap", tool.Wrap)
	}
}

// ptcWorkdir returns the sandbox work directory for this conversation.
//
// It must match the stub's _configure implementation exactly so run_code and
// run_shell share files.
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

// ptcBusinessContextArg extracts bkn_context.
//
// The lifecycle middleware has already validated it. The sandbox uses this
// value to select a working directory and to preserve the same interaction
// when it calls MCP.
func ptcBusinessContextArg(req mcp.CallToolRequest) map[string]any {
	if arguments := req.GetArguments(); arguments != nil {
		if value, ok := arguments["bkn_context"].(map[string]any); ok {
			return value
		}
		return map[string]any{}
	}
	raw, ok := req.GetRawArguments().(json.RawMessage)
	if !ok {
		return map[string]any{}
	}
	var decoded struct {
		BusinessContext map[string]any `json:"bkn_context"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded.BusinessContext == nil {
		return map[string]any{}
	}
	return decoded.BusinessContext
}
