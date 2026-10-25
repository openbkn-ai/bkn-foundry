// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	metricAppURI      = "ui://openbkn/object-metric-analysis"
	metricAppMIMEType = "text/html;profile=mcp-app"
	metricAppMaxBytes = 8 << 20
	metricAppEnv      = "MCP_METRIC_APP_HTML_URL"
)

// The HTML bundle belongs to Studio. Agent Retrieval only adapts the MCP
// resources/read protocol; it never builds or stores a product UI asset.
func metricAppHTMLURL() string {
	raw := strings.TrimSpace(os.Getenv(metricAppEnv))
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil || parsed.Host == "" || parsed.User != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		!strings.HasSuffix(parsed.Path, "/metric-analysis-mcp.html") ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return ""
	}
	return parsed.String()
}

func fullToolView(tool mcp.Tool) mcp.Tool {
	tool = modelContextToolView(tool)
	if tool.Name != toolKeyQueryMetric || metricAppHTMLURL() == "" {
		return tool
	}
	fields := map[string]any{}
	if tool.Meta != nil {
		for key, value := range tool.Meta.AdditionalFields {
			fields[key] = value
		}
	}
	fields["ui"] = map[string]any{"resourceUri": metricAppURI}
	tool.Meta = &mcp.Meta{AdditionalFields: fields}
	return tool
}

func registerMetricAppResource(srv *server.MCPServer) {
	address := metricAppHTMLURL()
	if address == "" {
		return
	}
	client := &http.Client{
		Timeout: 8 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	srv.AddResource(
		mcp.NewResource(metricAppURI, "Object Metric Analysis", mcp.WithMIMEType(metricAppMIMEType)),
		func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			if request.Params.URI != metricAppURI {
				return nil, fmt.Errorf("unknown metric app resource")
			}
			httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
			if err != nil {
				log.Printf("WARN: cannot create metric app resource request: %v", err)
				return nil, fmt.Errorf("metric app resource unavailable")
			}
			response, err := client.Do(httpRequest)
			if err != nil {
				log.Printf("WARN: cannot fetch metric app resource: %v", err)
				return nil, fmt.Errorf("metric app resource unavailable")
			}
			defer func() { _ = response.Body.Close() }()
			if response.StatusCode != http.StatusOK {
				return nil, fmt.Errorf("metric app resource unavailable: HTTP %d", response.StatusCode)
			}
			body, err := io.ReadAll(io.LimitReader(response.Body, metricAppMaxBytes+1))
			if err != nil {
				log.Printf("WARN: cannot read metric app resource: %v", err)
				return nil, fmt.Errorf("metric app resource unavailable")
			}
			if len(body) > metricAppMaxBytes || !strings.Contains(strings.ToLower(string(body[:min(len(body), 256)])), "<!doctype html>") {
				return nil, fmt.Errorf("metric app resource is not a valid single-file HTML bundle")
			}
			return []mcp.ResourceContents{mcp.TextResourceContents{
				URI:      metricAppURI,
				MIMEType: metricAppMIMEType,
				Text:     string(body),
				Meta: map[string]any{"ui": map[string]any{
					"csp": map[string]any{},
				}},
			}}, nil
		},
	)
}
