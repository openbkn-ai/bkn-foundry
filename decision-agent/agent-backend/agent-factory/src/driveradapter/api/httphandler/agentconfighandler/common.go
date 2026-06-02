package v3agentconfighandler

import (
	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/driveradapter/api/rdto/agent_config/agentconfigreq"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/infra/common/capimiddleware"
)

func setIsPrivate2Req(c *gin.Context, req *agentconfigreq.UpdateReq) {
	isPrivate := capimiddleware.IsInternalAPI(c)

	req.IsInternalAPI = isPrivate
}
