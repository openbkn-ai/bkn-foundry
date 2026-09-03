// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package audit

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/IBM/sarama"
	"github.com/bytedance/sonic"
	"github.com/google/uuid"

	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/mq"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

var (
	MAX_PRODUCER_RETRY              = 5               // Maximum retry count.
	NET_MAX_OPEN_REQUESTS           = 1               // Preserve message order by allowing one in-flight request.
	RECOVER_AUDIT_PRODUCER_INTERVAL = 2 * time.Minute // Recovery retry interval.

	// Audit log types.
	LOGIN      = "login"      // Login.
	OPERATION  = "operation"  // Operation.
	MANAGEMENT = "management" // Management.

	// Audit log levels.
	INFO = "INFO" // Informational.
	WARN = "WARN" // Warning.

	// Audit log statuses.
	SUCCESS = "success" // Success.
	FAILED  = "failed"  // Failure.

	// Audit operation types.
	CREATE   = "create"   // Create.
	DELETE   = "delete"   // Delete.
	UPDATE   = "update"   // Update.
	START    = "start"    // Start.
	STOP     = "stop"     // Stop.
	PAUSE    = "pause"    // Pause.
	ROLLOVER = "rollover" // Rollover.
	RECYCLE  = "recycle"  // Recycle.
	RECOVER  = "recover"  // Recover.

	AUDIT_TOPIC = "isf.audit_log.log"
)

var (
	DEFAULT_AUDIT_LOG_FROM = AuditLogFrom{
		Package: "",
		Service: AuditLogFromService{
			Name: "",
		},
	}
)

type AuditObject struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

type AuditOperator struct {
	Type  string             `json:"type"`
	ID    string             `json:"id"`
	Name  string             `json:"name,omitempty"`
	Agent AuditOperatorAgent `json:"agent"`
}

type AuditOperatorAgent struct {
	Type string `json:"type"`
	IP   string `json:"ip"`
	Mac  string `json:"mac"`
}

type AuditLogFrom struct {
	Package string              `json:"package"` // Package name.
	Service AuditLogFromService `json:"service"` // Service information.
}

type AuditLogFromService struct {
	Name string `json:"name"` // Service name.
}

type AuditLog struct {
	Type        string            `json:"type"`        // Log type.
	ID          string            `json:"out_biz_id"`  // Log ID.
	Level       string            `json:"level"`       // Log level.
	Operation   string            `json:"operation"`   // Operation type.
	Description string            `json:"description"` // Log description.
	OpTime      int64             `json:"op_time"`     // Operation timestamp.
	Operator    AuditOperator     `json:"operator"`    // Operator information.
	Object      AuditObject       `json:"object"`      // Target object information.
	LogFrom     AuditLogFrom      `json:"log_from"`    // Log source.
	Detail      map[string]string `json:"detail"`      // Details.

	Status string `json:"-"` // Status.
}

var (
	auditLogChan  chan *AuditLog = make(chan *AuditLog, 1000)
	auditProducer sarama.SyncProducer
)

func Init(mqSetting *mq.MQSetting) {

	// UT MODE, do nothing and return directly
	if os.Getenv("AUDIT_MODE_UT") == "true" {
		return
	}

	if mqSetting.MQType != "kafka" {
		logger.Errorf("audit Init failed, mq type is not kafka, mq type is %s", mqSetting.MQType)
		return
	}

	go initAuditLogHandler(mqSetting)
}

func TransforOperator(visitor hydra.Visitor) AuditOperator {
	var operatorType string
	switch visitor.Type {
	case hydra.VisitorType_RealName:
		operatorType = "authenticated_user"
	case hydra.VisitorType_Anonymous:
		operatorType = "anonymous_user"
	case hydra.VisitorType_App:
		operatorType = "app"
	}
	return AuditOperator{
		Type: operatorType,
		ID:   visitor.ID,
		Agent: AuditOperatorAgent{
			Type: string(visitor.ClientType),
			IP:   visitor.IP,
			Mac:  visitor.Mac,
		},
	}
}

// NewInfoLog creates an informational audit log.
func NewInfoLog(logType string, op string, operator AuditOperator, obj AuditObject, detail string) {
	auditLog := AuditLog{
		Type:      logType,
		Level:     INFO,
		Operation: op,
		OpTime:    time.Now().UnixNano(),
		Operator:  operator,
		Object:    obj,
		Status:    SUCCESS,
		Detail: map[string]string{
			"detail": detail,
		},
	}

	auditLogChan <- &auditLog
}

// NewWarnLog creates a warning audit log.
func NewWarnLog(logType string, op string, operator AuditOperator, obj AuditObject, status string, detail string) {
	auditLog := AuditLog{
		Type:      logType,
		Level:     WARN,
		Operation: op,
		OpTime:    time.Now().UnixNano(),
		Operator:  operator,
		Object:    obj,
		Status:    status,
		Detail: map[string]string{
			"detail": detail,
		},
	}

	auditLogChan <- &auditLog
}

// NewWarnLogWithError creates a warning audit log from an HTTP error.
func NewWarnLogWithError(logType string, op string, operator AuditOperator, obj AuditObject, err *rest.BaseError) {
	auditLog := AuditLog{
		Type:      logType,
		Level:     WARN,
		Operation: op,
		OpTime:    time.Now().UnixNano(),
		Operator:  operator,
		Object:    obj,
		Status:    FAILED,
		Detail: map[string]string{
			"detail": err.Error(),
		},
	}

	auditLogChan <- &auditLog
}

// initAuditLogHandler processes audit logs.
func initAuditLogHandler(mqSetting *mq.MQSetting) {

	auditProducer = getAuditProcuder(mqSetting, RECOVER_AUDIT_PRODUCER_INTERVAL)

	// Read the next audit log from the channel.
	for {
		auditLog := <-auditLogChan

		// Populate derived audit fields.
		if err := transformLog(auditLog); err != nil {
			logger.Errorf("generate audit log ID failed: %v", err)
			continue
		}

		// Send the audit log.
		sendLog(auditLog)
	}
}

// transformLog populates derived audit fields.
func transformLog(auditLog *AuditLog) error {
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	auditLog.ID = id.String()
	auditLog.LogFrom = DEFAULT_AUDIT_LOG_FROM

	var logInfoArr []string
	if auditLog.Operation != "" {
		logInfoArr = append(logInfoArr, auditLog.Operation)
	}
	if auditLog.Object.Type != "" {
		logInfoArr = append(logInfoArr, auditLog.Object.Type)
	}
	if auditLog.Object.Name != "" {
		logInfoArr = append(logInfoArr, auditLog.Object.Name)
	}
	if auditLog.Status != "" {
		logInfoArr = append(logInfoArr, auditLog.Status)
	}
	auditLog.Description = strings.Join(logInfoArr, " ")

	auditLog.Detail["status"] = auditLog.Status
	return nil
}

// sendLog sends an audit log to Kafka.
func sendLog(auditLog *AuditLog) {

	auditLogStr, err := sonic.MarshalString(auditLog)
	if err != nil {
		logger.Errorf("marshal auditLog failed: %v", err)
		return
	}

	logger.Infof("audit log: %v", auditLogStr)

	// Build the Kafka message.
	msg := &sarama.ProducerMessage{
		Topic: AUDIT_TOPIC,
		Value: sarama.StringEncoder(auditLogStr),
	}

	for {
		// Send the message.
		_, _, err = auditProducer.SendMessage(msg)
		if err == nil {
			return
		}
		logger.Errorf("send auditLog %v failed: %v, will try again", auditLog, err)
		time.Sleep(RECOVER_AUDIT_PRODUCER_INTERVAL)
	}
}

// newAuditProducer creates a Kafka producer.
func newAuditProducer(mqSetting *mq.MQSetting) (sarama.SyncProducer, error) {

	hosts := []string{fmt.Sprintf("%s:%d", mqSetting.MQHost, mqSetting.MQPort)}

	config := sarama.NewConfig()

	config.Net.SASL.Enable = true
	config.Net.SASL.Mechanism = sarama.SASLMechanism(mqSetting.Auth.Mechanism)
	config.Net.SASL.User = mqSetting.Auth.Username
	config.Net.SASL.Password = mqSetting.Auth.Password

	config.Producer.RequiredAcks = sarama.WaitForAll
	config.Producer.Partitioner = sarama.NewRandomPartitioner
	config.Producer.Return.Successes = true
	config.Producer.Retry.Max = MAX_PRODUCER_RETRY
	config.Net.MaxOpenRequests = NET_MAX_OPEN_REQUESTS

	// Connect to Kafka.
	producer, err := sarama.NewSyncProducer(hosts, config)
	if err != nil {
		logger.Errorf("can not connect to kafka ,create kafka producer failed: %v", err)
		return nil, err
	}

	logger.Debugf("Create producer on topic %s", AUDIT_TOPIC)
	return producer, nil
}

// getAuditProcuder obtains an audit producer.
// It retries after the interval so the producer reconnects when Kafka recovers.
func getAuditProcuder(mqSetting *mq.MQSetting, interval time.Duration) sarama.SyncProducer {

	logger.Infof("get auditProducer if auditProducer is nil, interval: %s", interval)
	for {
		producer, err := newAuditProducer(mqSetting)
		if err != nil {
			logger.Errorf("can not connect to kafka, create kafka producer failed: %v", err)
		}
		if producer != nil {
			return producer
		}
		time.Sleep(interval)
	}
}
