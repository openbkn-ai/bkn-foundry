// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcptool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/openbkn-ai/licverify"
)

// HTTPManifest is a provider-owned catalogue. Schemas and business handlers stay
// in the provider; the shared MCP service only adapts authenticated transport.
type HTTPManifest struct {
	CallPath string     `json:"call_path"`
	Tools    []HTTPTool `json:"tools"`
}

type HTTPTool struct {
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	Capability   string            `json:"capability"`
	MinEdition   licverify.Edition `json:"min_edition"`
	ControlPlane bool              `json:"control_plane"`
	Group        string            `json:"group"`
	Input        json.RawMessage   `json:"input_schema"`
}

// LoadHTTPProvider is opt-in and runs before the assembly registry freezes.
// A configured but unavailable provider fails startup instead of advertising
// tools that cannot execute. The URL is operator configuration, never user input.
func LoadHTTPProvider(ctx context.Context, endpoint string, client *http.Client) ([]ExtraTool, error) {
	base, bounded, err := httpProviderConfig(endpoint, client)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return nil, err
	}
	response, err := bounded.Do(req)
	if err != nil {
		return nil, errors.New("MCP extension provider is unavailable")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 256*1024+1))
	if err != nil || len(body) > 256*1024 || response.StatusCode != http.StatusOK {
		return nil, errors.New("cannot read MCP extension manifest")
	}
	return LoadHTTPProviderManifest(endpoint, body, client)
}

// LoadConfiguredHTTPProvider uses a provider-owned deployment artifact when
// supplied. Registration must finish before Freeze even without a licence;
// discovery must not require exposing the provider's paid HTTP surface.
func LoadConfiguredHTTPProvider(ctx context.Context, endpoint string, manifest []byte, client *http.Client) ([]ExtraTool, error) {
	if len(manifest) > 0 {
		return LoadHTTPProviderManifest(endpoint, manifest, client)
	}
	return LoadHTTPProvider(ctx, endpoint, client)
}

// LoadHTTPProviderManifest shares the HTTP loader's validation and authenticated
// same-origin forwarding. Configured schemas never authorize execution.
func LoadHTTPProviderManifest(endpoint string, body []byte, client *http.Client) ([]ExtraTool, error) {
	base, bounded, err := httpProviderConfig(endpoint, client)
	if err != nil {
		return nil, err
	}
	if len(body) > 256*1024 {
		return nil, errors.New("invalid MCP extension manifest")
	}
	var manifest HTTPManifest
	if json.Unmarshal(body, &manifest) != nil || len(manifest.Tools) == 0 || len(manifest.Tools) > 32 || !strings.HasPrefix(manifest.CallPath, "/api/") || strings.ContainsAny(manifest.CallPath, "?#") || strings.Contains(manifest.CallPath, "..") {
		return nil, errors.New("invalid MCP extension manifest")
	}
	callURL := *base
	callURL.Path = manifest.CallPath
	seen := map[string]bool{}
	tools := make([]ExtraTool, 0, len(manifest.Tools))
	for _, definition := range manifest.Tools {
		if definition.Name == "" || len(definition.Name) > 100 || seen[definition.Name] || definition.Capability == "" || !json.Valid(definition.Input) ||
			(definition.MinEdition != licverify.EditionProfessional && definition.MinEdition != licverify.EditionEnterprise) {
			return nil, errors.New("invalid MCP extension tool")
		}
		var input map[string]any
		if json.Unmarshal(definition.Input, &input) != nil || input["type"] != "object" {
			return nil, errors.New("invalid MCP extension input schema")
		}
		seen[definition.Name] = true
		toolName := definition.Name
		tools = append(tools, ExtraTool{Key: toolName, Name: toolName, Desc: definition.Description, Capability: definition.Capability, MinEdition: definition.MinEdition, Input: definition.Input, Group: definition.Group, ControlPlane: definition.ControlPlane,
			Handle: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				// Credentials come only from the authenticated transport, never
				// arguments or a provider-supplied URL. The provider rechecks them.
				authorization := request.Header.Get("Authorization")
				if authorization == "" || strings.ContainsAny(authorization, "\r\n") {
					return mcp.NewToolResultError("Authentication is required"), nil
				}
				payload, err := json.Marshal(map[string]any{"name": toolName, "arguments": request.GetArguments()})
				if err != nil || len(payload) > 1024*1024 {
					return mcp.NewToolResultError("Invalid extension arguments"), nil
				}
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, callURL.String(), bytes.NewReader(payload))
				if err != nil {
					return nil, err
				}
				req.Header.Set("Authorization", authorization)
				req.Header.Set("Content-Type", "application/json")
				resp, err := bounded.Do(req)
				if err != nil {
					return mcp.NewToolResultError("Extension provider is unavailable; retry with the same request identity"), nil
				}
				defer resp.Body.Close()
				data, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024+1))
				if err != nil || len(data) > 2*1024*1024 || !json.Valid(data) {
					return mcp.NewToolResultError("Invalid extension response"), nil
				}
				if resp.StatusCode >= 500 {
					return mcp.NewToolResultError("Extension request is unavailable; retry with the same request identity"), nil
				}
				if resp.StatusCode >= 400 {
					return mcp.NewToolResultError(string(data)), nil
				}
				var content any
				decoder := json.NewDecoder(bytes.NewReader(data))
				decoder.UseNumber()
				if err := decoder.Decode(&content); err != nil {
					return nil, fmt.Errorf("decode extension response: %w", err)
				}
				return &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent(string(data))}, StructuredContent: content}, nil
			},
		})
	}
	return tools, nil
}

func httpProviderConfig(endpoint string, client *http.Client) (*url.URL, *http.Client, error) {
	base, err := url.Parse(endpoint)
	if err != nil || base == nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, nil, errors.New("invalid MCP extension manifest URL")
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	bounded := *client
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return base, &bounded, nil
}
