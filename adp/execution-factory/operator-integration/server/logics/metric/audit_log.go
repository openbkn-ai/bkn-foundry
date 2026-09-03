// Package metric
// @description: Metric model operation interface.
// @file metric.go
package metric

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/localize"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
)

// constant definition.
const (
	RecorderDIP    = "DIP"                        // Products.
	PackageName    = "DataOperatorHub"            // package name.
	OperationType  = "operation"                  // Operation log type.
	ServiceName    = "agent-operator-integration" // Service name.
	ExMsgLimit     = 65535                        // Maximum length of additional information.
	DefaultTimeout = 30 * time.Second             // Default timeout.
)

// AuditLogOperationModel audit operation log model.
type AuditLogOperationModel struct {
	Operation   AuditLogOperationType `json:"operation" validate:"required"`          // Operation type.
	Description string                `json:"description" validate:"required"`        // String description, maximum length 65,535.
	OpTime      int64                 `json:"op_time" validate:"required"`            // Operation time (required for reporting through mq) is accurate to nanoseconds.
	Operator    AuditLogOperatorInfo  `json:"operator" validate:"required"`           // Operator information.
	Object      AuditLogObject        `json:"object,omitempty"`                       // Operation object information.
	LogFrom     LogFrom               `json:"log_from" validate:"required"`           // Log source.
	Detail      interface{}           `json:"detail,omitempty"`                       // Details.
	ExMsg       string                `json:"ex_msg,omitempty"`                       // Additional information, maximum length 65,535.
	Level       LoggerLevel           `json:"level" validate:"required"`              // Log level, default INFO.
	OutBizID    string                `json:"out_biz_id" validate:"required,max=128"` // External unique business ID, used for anti-shake, format is not limited, up to 128.
	Type        string                `json:"type" validate:"required"`               // Log type, maximum length 128.
}

// LogFrom log source.
type LogFrom struct {
	Package string      `json:"package" validate:"required"` // Big package name.
	Service ServiceInfo `json:"service" validate:"required"` // Service information.
}

// ServiceInfo service information.
type ServiceInfo struct {
	Name string `json:"name" validate:"required"` // Service name.
}

// LoggerLevel log level.
type LoggerLevel string

const (
	LoggerLevelInfo LoggerLevel = "INFO" // information.
	LoggerLevelWarn LoggerLevel = "WARN" // warning.
)

// AuditLogObjectType Audit log operation object type.
type AuditLogObjectType string

const (
	AuditLogObjectOperator AuditLogObjectType = "operator" // operator.
	AuditLogObjectTool     AuditLogObjectType = "tool"     // Tools.
	AuditLogObjectMCP      AuditLogObjectType = "mcp"      // mcp
)

// AuditLogObject operation object information.
type AuditLogObject struct {
	Type AuditLogObjectType `json:"type" validate:"required"` // Operate type.
	Name string             `json:"name"`                     // Operation object name, maximum length 128.
	ID   string             `json:"id"`                       // Operation object ID, maximum length 40.
}

// NewAuditLogObject creates operation object information.
func NewAuditLogObject(typ AuditLogObjectType, name, id string) *AuditLogObject {
	return &AuditLogObject{
		Type: typ,
		Name: name,
		ID:   id,
	}
}

// AuditLogOperatoAgent operator agent information.
type AuditLogOperatoAgent struct {
	Type string `json:"type" validate:"required"` // Operator client type.
	IP   string `json:"ip" validate:"required"`   // Operator device IP.
	MAC  string `json:"mac" validate:"required"`  // Operator device mac address.
}

// AuditLogOperatorInfo operator information.
type AuditLogOperatorInfo struct {
	ID    string               `json:"id" validate:"required,max=40"`    // Operator ID, maximum length 40.
	Name  string               `json:"name" validate:"required,max=128"` // Operator name, subject to the incoming data, the maximum length is 128, type is internal_service and must be passed.
	Type  AuditLogOperatorType `json:"type" validate:"required"`         // Operator type.
	Agent AuditLogOperatoAgent `json:"agent" validate:"required"`        // Operator agent information.
}

// AuditLogOperatorType operator type.
type AuditLogOperatorType string

const (
	AuthenticatedUser AuditLogOperatorType = "authenticated_user" // Real-name user.
	AnonymousUser     AuditLogOperatorType = "anonymous_user"     // anonymous user.
	AppUser           AuditLogOperatorType = "app"                // application account.
	InternalService   AuditLogOperatorType = "internal_service"   // Internal services.
)

// Validate verifies whether the operator type is legal.
func (a AuditLogOperatorType) Validate() error {
	validTypeMap := map[AuditLogOperatorType]struct{}{
		AuthenticatedUser: {},
		AnonymousUser:     {},
		AppUser:           {},
		InternalService:   {},
	}
	for t := range validTypeMap {
		if a == t {
			return nil
		}
	}
	return fmt.Errorf("invalid operator type %s", a)
}

// AuditLogOperationType Audit log operation type.
type AuditLogOperationType string

const (
	AuditLogOperationCreate    AuditLogOperationType = "create"    // New.
	AuditLogOperationDelete    AuditLogOperationType = "delete"    // Delete.
	AuditLogOperationEdit      AuditLogOperationType = "edit"      // Edit.
	AuditLogOperationPublish   AuditLogOperationType = "publish"   // publish.
	AuditLogOperationUnpublish AuditLogOperationType = "unpublish" // Unpublish (remove)
	AuditLogOperationExecute   AuditLogOperationType = "execute"   // execute.
)

// AuditLogBuilder audit log builder.
type AuditLogBuilder struct {
	ts                 *localize.I18nTranslator
	logger             interfaces.Logger
	topic              string
	outboxMessageEvent interfaces.IOutboxMessageEvent
}

// NewAuditLogBuilder creates an audit log builder.
func NewAuditLogBuilder() *AuditLogBuilder {
	return &AuditLogBuilder{
		ts:                 localize.NewI18nTranslator(config.NewConfigLoader().Project.Language),
		logger:             config.NewConfigLoader().GetLogger(),
		topic:              interfaces.AuditLogTopic,
		outboxMessageEvent: common.NewOutboxMessageEvent(),
	}
}

// AuditLogBuilderParams audit log building parameters.
type AuditLogBuilderParams struct {
	TokenInfo    *interfaces.TokenInfo    // Token information.
	Accessor     *interfaces.AuthAccessor // Visitor information.
	Operation    AuditLogOperationType    // Operation type.
	Object       *AuditLogObject          // Operation object.
	Description  string                   // Description information.
	ExMsg        string                   // Exception information.
	Detils       interface{}              // Operation details.
	OperatorType AuditLogOperatorType     // Operator type.
}

// AuditLogToolDetil tool operation details.
type AuditLogToolDetil struct {
	ToolID   string `json:"tool_id"`   // Tool ID.
	ToolName string `json:"tool_name"` // Tool name.
}

// AuditLogToolDetils tool operation details.
type AuditLogToolDetils struct {
	Infos         []AuditLogToolDetil `json:",inline"`
	OperationCode OperationCode
}

// NewAuditLogToolDetils Create tool operation details.
func NewAuditLogToolDetils(operationCode OperationCode, infos []AuditLogToolDetil) *AuditLogToolDetils {
	return &AuditLogToolDetils{
		Infos:         infos,
		OperationCode: operationCode,
	}
}

// OperationCode operation code.
type OperationCode string

// Tool additional information.
const (
	// "Import tool "%s" from operator successfully".
	ImportToolFromOperator OperationCode = "import_tool_from_operator"
	// "Add tool "%s" to toolbox successfully".
	AddTool OperationCode = "add_tool"
	// "Editing tool "%s" successful".
	EditTool OperationCode = "edit_tool"
	// "Tool "%s" removed from toolbox successfully".
	DeleteTool OperationCode = "remove_tool"
	// "Update tool status "%s" successful".
	UpdateToolStatus OperationCode = "update_tool_status"
	// "Debugging tool "%s" successful",
	DebugTool OperationCode = "debug_tool"
	// "Execute tool "%s" successfully".
	ExecuteTool OperationCode = "execute_tool"
	// Unknown operation.
	UnknownOperation OperationCode = "unknown_operation"
)

func (b *AuditLogBuilder) getToolDetailsAndExMsg(param interface{}) (detils interface{}, exMsg string) {
	if param == nil {
		return
	}
	p, ok := param.(*AuditLogToolDetils)
	if !ok {
		b.logger.Errorf("invalid detils type")
		return
	}
	if len(p.Infos) == 0 {
		return
	}
	detils = map[string]interface{}{
		"tool_infos": p.Infos,
	}
	var toolNames string
	for i, info := range p.Infos {
		if info.ToolName == "" {
			continue
		}
		if i == 0 {
			toolNames += info.ToolName
			continue
		}
		toolNames += "," + info.ToolName
	}
	switch p.OperationCode {
	case ImportToolFromOperator, AddTool, EditTool, DeleteTool, UpdateToolStatus, DebugTool, ExecuteTool:
		exMsg = fmt.Sprintf(b.ts.Trans(fmt.Sprintf("audit_log.%s", p.OperationCode)), toolNames)
	case UnknownOperation:
		exMsg = fmt.Sprintf(b.ts.Trans(fmt.Sprintf("audit_log.%s", UnknownOperation)), toolNames)
	default:
		exMsg = fmt.Sprintf(b.ts.Trans(fmt.Sprintf("audit_log.%s", UnknownOperation)), toolNames)
	}
	if len(exMsg) > ExMsgLimit {
		exMsg = exMsg[:ExMsgLimit]
	}
	return
}

// GetOperatorType Gets the operator type.
func (p *AuditLogBuilderParams) GetOperatorType() error {
	if p.OperatorType != "" {
		return p.OperatorType.Validate()
	}
	if p.TokenInfo == nil {
		return fmt.Errorf("token info is nil")
	}
	var operatorType AuditLogOperatorType
	switch p.TokenInfo.VisitorTyp {
	case interfaces.RealName:
		operatorType = AuthenticatedUser
	case interfaces.Anonymous:
		operatorType = AnonymousUser
	case interfaces.Business:
		operatorType = AppUser
	default:
		operatorType = InternalService
	}
	p.OperatorType = operatorType
	return nil
}

// Build build audit log model.
func (b *AuditLogBuilder) build(p *AuditLogBuilderParams) (interface{}, error) {
	if p.TokenInfo == nil {
		return nil, fmt.Errorf("token info is nil")
	}
	if p.Accessor == nil {
		return nil, fmt.Errorf("accessor is nil")
	}
	err := p.GetOperatorType()
	if err != nil {
		return nil, err
	}
	var level LoggerLevel
	switch p.Operation {
	case AuditLogOperationCreate, AuditLogOperationEdit, AuditLogOperationPublish,
		AuditLogOperationUnpublish, AuditLogOperationExecute:
		level = LoggerLevelInfo
	case AuditLogOperationDelete:
		level = LoggerLevelWarn
	default:
		return nil, fmt.Errorf("invalid operation type")
	}
	// organization.
	outBizID, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	logObj := &AuditLogOperationModel{
		Operation:   p.Operation,
		Description: p.Description,
		OpTime:      time.Now().UnixNano(),
		Operator: AuditLogOperatorInfo{
			Type: p.OperatorType,
			Name: p.Accessor.Name,
			ID:   p.Accessor.ID,
		},
		LogFrom: LogFrom{
			Package: PackageName,
			Service: ServiceInfo{
				Name: ServiceName,
			},
		},
		Detail:   p.Detils,
		ExMsg:    p.ExMsg,
		Level:    level,
		OutBizID: outBizID.String(),
		Type:     OperationType,
	}
	// Internal services do not record Agent information.
	if logObj.Operator.Type == AuthenticatedUser || logObj.Operator.Type == AnonymousUser || logObj.Operator.Type == AppUser {
		logObj.Operator.Agent = AuditLogOperatoAgent{
			Type: p.TokenInfo.ClientTyp.String(),
			IP:   p.TokenInfo.LoginIP,
			MAC:  p.TokenInfo.MAC,
		}
	}
	if p.Object == nil {
		return logObj, nil
	}
	logObj.Object = *p.Object
	if logObj.Description != "" {
		return logObj, nil
	}
	logObj.Description = b.ts.Trans(fmt.Sprintf("audit_log.%s_%s", p.Operation, p.Object.Type),
		p.Object.Name)
	switch p.Object.Type {
	case AuditLogObjectOperator, AuditLogObjectMCP:
		logObj.Description = fmt.Sprintf(b.ts.Trans(fmt.Sprintf("audit_log.%s_%s", p.Object.Type, p.Operation)), p.Object.Name)
	case AuditLogObjectTool:
		logObj.Detail, logObj.ExMsg = b.getToolDetailsAndExMsg(logObj.Detail)
		logObj.Description = fmt.Sprintf(b.ts.Trans(fmt.Sprintf("audit_log.%s_%s", p.Object.Type, p.Operation)), p.Object.Name)
	}
	return logObj, nil
}

// Logger records audit logs.
func (b *AuditLogBuilder) Logger(ctx context.Context, p *AuditLogBuilderParams) {
	if !config.GetAuthEnabled() {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	newCtx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel() // Ensure resource release.
	logObj, err := b.build(p)
	if err != nil {
		b.logger.WithContext(newCtx).Errorf("build audit log failed: %v", err)
		return
	}
	eventID, err := uuid.NewV7()
	if err != nil {
		b.logger.WithContext(newCtx).Errorf("generate audit event UUIDv7 failed: %v", err)
		return
	}
	err = b.outboxMessageEvent.Publish(newCtx, &interfaces.OutboxMessageReq{
		EventID:   eventID.String(),
		EventType: interfaces.OutboxMessageEventTypeAuditLog,
		Topic:     interfaces.AuditLogTopic,
		Payload:   utils.ObjectToJSON(logObj),
	})
	if err != nil {
		b.logger.WithContext(newCtx).Errorf("write audit log failed: %v", err)
	}
}
