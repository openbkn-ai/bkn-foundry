package productreq

import (
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/driveradapter/api/rdto/common"
)

type ListReq struct {
	common.PageSize
}
