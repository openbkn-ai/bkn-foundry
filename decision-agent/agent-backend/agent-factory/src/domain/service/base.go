package service

import (
	"github.com/kweaver-ai/kweaver-go-lib/logger"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/infra/cmp/icmp"
)

type SvcBase struct {
	Logger icmp.Logger
}

func NewSvcBase() *SvcBase {
	return &SvcBase{
		Logger: logger.GetLogger(),
	}
}
