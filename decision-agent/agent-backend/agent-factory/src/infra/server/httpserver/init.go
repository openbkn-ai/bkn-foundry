package httpserver

import (
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/driveradapter/api"
	v3agentconfighandler "github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/driveradapter/api/httphandler/agentconfighandler"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/driveradapter/api/httphandler/anysharedshandler"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/driveradapter/api/httphandler/categoryhandler"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/driveradapter/api/httphandler/otherhandler"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/driveradapter/api/httphandler/permissionhandler"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/driveradapter/api/httphandler/personalspacehandler"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/driveradapter/api/httphandler/producthandler"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/driveradapter/api/httphandler/publishedhandler"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/driveradapter/api/httphandler/releasehandler"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/driveradapter/api/httphandler/squarehandler"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/driveradapter/api/httphandler/testhandler"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/driveradapter/api/httphandler/tplhandler"

	// Run侧 handler
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/driveradapter/api/httphandler/agenthandler"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/driveradapter/api/httphandler/conversationhandler"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/driveradapter/api/httphandler/sessionhandler"
)

// NewHTTPServer 创建HTTP服务器实例
func NewHTTPServer() IServer {
	s := &httpServer{
		// 健康检查
		httpHealthHandler: api.NewHTTPHealthHandler(),

		// Management侧 (V3)
		v3AgentConfigHandler: v3agentconfighandler.NewDAConfHTTPHandler(),
		v3AgentTplHandler:    tplhandler.NewDATplHTTPHandler(),
		productHandler:       producthandler.NewProductHTTPHandler(),
		categoryHandler:      categoryhandler.NewCategoryHandler(),
		releaseHandler:       releasehandler.NewReleaseHandler(),
		squareHandler:        squarehandler.NewSquareHandler(),
		publishedHandler:     publishedhandler.NewPublishedHandler(),
		permissionHandler:    permissionhandler.NewPermissionHandler(),
		personalSpaceHandler: personalspacehandler.GetPersonalSpaceHTTPHandler(),
		otherHandler:         otherhandler.NewOtherHTTPHandler(),
		testHandler:          testhandler.NewTestHTTPHandler(),
		anysharedsHandler:    anysharedshandler.NewAnysharedsHandler(),

		// Run侧 (V1)
		agentHandler:        agenthandler.NewAgentHTTPHandler(),
		conversationHandler: conversationhandler.NewConversationHTTPHandler(),
		sessionHandler:      sessionhandler.NewSessionHTTPHandler(),
	}

	return s
}
