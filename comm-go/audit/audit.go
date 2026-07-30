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
	"github.com/rs/xid"

	"github.com/openbkn-ai/bkn-comm-go/hydra"
	"github.com/openbkn-ai/bkn-comm-go/logger"
	"github.com/openbkn-ai/bkn-comm-go/mq"
	"github.com/openbkn-ai/bkn-comm-go/rest"
)

var (
	MAX_PRODUCER_RETRY              = 5               // 重试次数
	NET_MAX_OPEN_REQUESTS           = 1               // 设置为 1, 确保某一时刻只能发送一个请求, 避免因为 retry 导致的消息乱序
	RECOVER_AUDIT_PRODUCER_INTERVAL = 2 * time.Minute //时间间隔

	// 日志类型
	LOGIN      = "login"      // 登录
	OPERATION  = "operation"  // 操作
	MANAGEMENT = "management" // 管理

	// 日志级别
	INFO = "INFO" // 信息
	WARN = "WARN" // 警告

	// 日志状态
	SUCCESS = "success" // 成功
	FAILED  = "failed"  // 失败

	// 操作类型
	CREATE   = "create"   // 新建
	DELETE   = "delete"   // 删除
	UPDATE   = "update"   // 修改
	START    = "start"    // 开始
	STOP     = "stop"     // 停止
	PAUSE    = "pause"    // 暂停
	ROLLOVER = "rollover" // 轮转
	RECYCLE  = "recycle"  // 回收
	RECOVER  = "recover"  // 恢复

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
	Package string              `json:"package"` // pakcgae名称
	Service AuditLogFromService `json:"service"` // service
}

type AuditLogFromService struct {
	Name string `json:"name"` // service名称
}

type AuditLog struct {
	Type        string            `json:"type"`        // 日志类型
	ID          string            `json:"out_biz_id"`  // 日志ID
	Level       string            `json:"level"`       // 日志级别
	Operation   string            `json:"operation"`   // 操作类型
	Description string            `json:"description"` // 日志描述
	OpTime      int64             `json:"op_time"`     // 操作时间
	Operator    AuditOperator     `json:"operator"`    // 操作者信息
	Object      AuditObject       `json:"object"`      // 操作对象信息
	LogFrom     AuditLogFrom      `json:"log_from"`    // 日志来源
	Detail      map[string]string `json:"detail"`      // 详情

	Status string `json:"-"` // 状态
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

// 创建信息级别的审计日志
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

// 创建警告级别的审计日志
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

// 创建警告级别的审计日志
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

// 处理审计日志
func initAuditLogHandler(mqSetting *mq.MQSetting) {

	auditProducer = getAuditProcuder(mqSetting, RECOVER_AUDIT_PRODUCER_INTERVAL)

	//从channel中取数据
	for {
		auditLog := <-auditLogChan

		// 处理审计日志
		transformLog(auditLog)

		// 发送审计日志
		sendLog(auditLog)
	}
}

// 处理审计日志
func transformLog(auditLog *AuditLog) {
	auditLog.ID = xid.New().String()
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
}

// 往kafka发送审计日志
func sendLog(auditLog *AuditLog) {

	auditLogStr, err := sonic.MarshalString(auditLog)
	if err != nil {
		logger.Errorf("marshal auditLog failed: %v", err)
		return
	}

	logger.Infof("audit log: %v", auditLogStr)

	// 构造一个消息
	msg := &sarama.ProducerMessage{
		Topic: AUDIT_TOPIC,
		Value: sarama.StringEncoder(auditLogStr),
	}

	for {
		// 发送消息
		_, _, err = auditProducer.SendMessage(msg)
		if err == nil {
			return
		}
		logger.Errorf("send auditLog %v failed: %v, will try again", auditLog, err)
		time.Sleep(RECOVER_AUDIT_PRODUCER_INTERVAL)
	}
}

// 新建kafka生产者
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

	// 连接kafka
	producer, err := sarama.NewSyncProducer(hosts, config)
	if err != nil {
		logger.Errorf("can not connect to kafka ,create kafka producer failed: %v", err)
		return nil, err
	}

	logger.Debugf("Create producer on topic %s", AUDIT_TOPIC)
	return producer, nil
}

// 获取auditProducer
// 若获取auditProducer为nil时，过两分钟继续获取，实现kafka恢复正常后自动连接
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
