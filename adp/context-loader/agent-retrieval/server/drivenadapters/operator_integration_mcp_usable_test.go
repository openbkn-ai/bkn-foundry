// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/mocks"
)

// An MCP Server's tools are callable while it is published and while it is editing: an editing
// server is served from its release (bkn-foundry#1478), so search keeps offering its tools and
// execute_tool keeps running them (#1524). Every other state, and an unreadable server, is not.
func TestMCPServerIsUsable(t *testing.T) {
	cases := []struct {
		name   string
		code   int
		status string
		want   bool
	}{
		{"published", 200, "published", true},
		{"editing", 200, "editing", true},
		{"offline", 200, "offline", false},
		{"unpublish", 200, "unpublish", false},
		{"unreadable", 404, "", false},
	}
	for _, c := range cases {
		convey.Convey("MCP Server "+c.name, t, func() {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			logger := mocks.NewMockLogger(ctrl)
			logger.EXPECT().WithContext(gomock.Any()).Return(logger).AnyTimes()
			logger.EXPECT().Warnf(gomock.Any(), gomock.Any()).AnyTimes()
			httpClient := mocks.NewMockHTTPClient(ctrl)
			httpClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(c.code, map[string]interface{}{"base_info": map[string]interface{}{"status": c.status}}, nil)
			client := &operatorIntegrationClient{
				logger:     logger,
				baseURL:    "http://localhost:8080/api/agent-operator-integration",
				httpClient: httpClient,
			}

			usable, err := client.MCPServerIsUsable(context.Background(), "mcp-1")
			convey.So(err, convey.ShouldBeNil)
			convey.So(usable, convey.ShouldEqual, c.want)
		})
	}
}
