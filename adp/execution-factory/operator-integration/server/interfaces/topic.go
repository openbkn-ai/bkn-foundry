package interfaces

// This file defines Topic constants and records them uniformly to facilitate statistics.

const (
	// ChannelMessage operator integrated event Channel.
	ChannelMessage = "operator_integration" // channel
)

// Listen to external event Topic list.
const (
	// AuthResourceNameModifyTopic Resource name change Topic.
	AuthResourceNameModifyTopic = "authorization.resource.name.modify"
	// AuditLogTopic audit log topic.
	AuditLogTopic = "isf.audit_log.log"
)

// Notify external event Topick list.

const (
	// OperatorDeleteEventTopic Operator deletes event Topic.
	OperatorDeleteEventTopic = "agent_operator_integration.operator.delete"
)

// OperatorDeleteEvent Operator delete event.
type OperatorDeleteEvent struct {
	OperatorID   string                 `json:"operator_id"`
	Version      string                 `json:"version"`
	Status       BizStatus              `json:"status"`
	IsInternal   bool                   `json:"is_internal"`                                          // Is it an internal operator?.
	IsDataSource bool                   `json:"is_data_source" form:"is_data_source" default:"false"` // Whether it is a data source operator.
	ExtendInfo   map[string]interface{} `json:"extend_info"`
	OperatorType OperatorType           `json:"operator_type" form:"operator_type" default:"basic" validate:"oneof=basic composite"` // Operator type (basic/composite.
	UpdateUser   string                 `json:"update_user"`
}
