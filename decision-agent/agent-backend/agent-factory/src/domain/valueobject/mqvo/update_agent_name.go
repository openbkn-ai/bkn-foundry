package mqvo

import "github.com/kowell-ai/kowell-core/decision-agent/agent-backend/agent-factory/src/domain/enum/cdaenum"

type UpdateAgentNameMqMsg struct {
	ID   string               `json:"id"`
	Type cdaenum.ResourceType `json:"type"`
	Name string               `json:"name"`
}

func NewUpdateAgentNameMqMsg(id string, name string) *UpdateAgentNameMqMsg {
	return &UpdateAgentNameMqMsg{
		ID:   id,
		Type: cdaenum.ResourceTypeDataAgent,
		Name: name,
	}
}
