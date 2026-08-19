package driveradapters

import (
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/mq"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/toolbox"
)

type mqHandler struct {
	MQClient            mq.MQClient
	ToolboxEventHandler interfaces.ToolBoxEventHandler
	Logger              interfaces.Logger
}

var (
	mqOnce            sync.Once
	mqHandlerInstance interfaces.MQHandler
)

// NewMQHandler creates MQ processing interface.
func NewMQHandler() interfaces.MQHandler {
	mqOnce.Do(func() {
		conf := config.NewConfigLoader()
		mqHandlerInstance = &mqHandler{
			MQClient:            mq.NewMQClient(),
			ToolboxEventHandler: toolbox.NewToolServiceImpl(),
			Logger:              conf.GetLogger(),
		}
	})
	return mqHandlerInstance
}

// To-be-processed Topic list.
var pendingTopics = []string{
	interfaces.OperatorDeleteEventTopic,
}

// Subscribe Subscribe to events.
func (h *mqHandler) Subscribe() {
	for _, topic := range pendingTopics {
		switch topic {
		case interfaces.OperatorDeleteEventTopic:
			h.MQClient.Subscribe(topic, interfaces.ChannelMessage, h.ToolboxEventHandler.HandleOperatorDeleteEvent)
		default:
			h.Logger.Errorf("unknown topic: %s", topic)
		}
	}
}
