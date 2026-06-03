package daresvo

import (
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/domain/enum/chat_enum/chatresenum"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/domain/valueobject/agentrespvo"
)

// DocRetrievalResultStrategy 文档召回结果判断策略接口
type DocRetrievalResultStrategy interface {
	// Process 处理结果并返回标准化的结构
	Process(answer interface{}) (agentrespvo.DocRetrievalAnswer, error)

	// GetStrategyName 获取策略名称
	GetStrategyName() chatresenum.DocRetrievalStrategy
}
