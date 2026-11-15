package drivenadapters

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

const paginationToolCount = 5

// newPaginatedMCPServer serves paginationToolCount tools two per page, so a client that reads
// only the first page sees two of them.
func newPaginatedMCPServer() *server.MCPServer {
	srv := server.NewMCPServer("paginated", "1.0.0", server.WithPaginationLimit(2))
	for i := 0; i < paginationToolCount; i++ {
		srv.AddTool(mcp.NewTool(fmt.Sprintf("tool_%d", i)),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return mcp.NewToolResultText("pong:" + req.Params.Name), nil
			})
	}
	return srv
}

// TestListToolsFollowsPagination guards the mcp-go upgrade: a server that paginates tools/list
// must still be listed in full, and CallTool must keep working against it.
func TestListToolsFollowsPagination(t *testing.T) {
	ctx := context.Background()

	t.Run("in-process", func(t *testing.T) {
		cli, err := NewInProcessMCPClient(ctx, newPaginatedMCPServer())
		if err != nil {
			t.Fatalf("NewInProcessMCPClient() error = %v", err)
		}
		defer cli.Close()
		assertAllToolsListed(t, ctx, cli)
	})

	t.Run("streamable http", func(t *testing.T) {
		ts := httptest.NewServer(server.NewStreamableHTTPServer(newPaginatedMCPServer()))
		defer ts.Close()
		// Build the client directly: NewMCPClient needs the service config loader.
		raw, err := client.NewStreamableHttpClient(ts.URL)
		if err != nil {
			t.Fatalf("NewStreamableHttpClient() error = %v", err)
		}
		cli := &MCPClient{client: raw}
		if err := cli.performHandshake(ctx); err != nil {
			t.Fatalf("performHandshake() error = %v", err)
		}
		defer cli.Close()
		assertAllToolsListed(t, ctx, cli)
	})
}

func assertAllToolsListed(t *testing.T, ctx context.Context, cli interfaces.MCPClient) {
	t.Helper()
	res, err := cli.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(res.Tools) != paginationToolCount {
		t.Fatalf("ListTools() returned %d tools, want %d (pagination not followed)", len(res.Tools), paginationToolCount)
	}
	if res.NextCursor != "" {
		t.Fatalf("ListTools() NextCursor = %q, want empty", res.NextCursor)
	}

	call := mcp.CallToolRequest{}
	call.Params.Name = "tool_4"
	out, err := cli.CallTool(ctx, call)
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if out.IsError || len(out.Content) != 1 {
		t.Fatalf("CallTool() = %+v, want one non-error content", out)
	}
}
