package common

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/creasty/defaults"
	validator "github.com/go-playground/validator/v10"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/dbaccess"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/lock"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/mq"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	redis "github.com/redis/go-redis/v9"
)

const (
	lockKeyTemp          = "agent-operator-integration:outbox_message_event:lock" // Lock key template.
	defultLockExpiryTime = 1 * time.Minute                                        // Default lock expiration time.
	commonPollInterval   = 3 * time.Second                                        // Scan interval.
	queryDefaultLimit    = 100                                                    // Default query quantity.
	defaultTimeout       = 30 * time.Second                                       // Default timeout.
)

var (
	outboxOnce  sync.Once
	outboxEvent *outboxMessageEvent
)

// OutboxMessageEvent message event management.
type outboxMessageEvent struct {
	confLoader      *config.Config
	logger          interfaces.Logger
	outboxMessageDB model.IOutboxMessage
	mqClient        mq.MQClient
	redisCli        *redis.Client
	quit            chan bool
}

// NewOutboxMessageEvent creates message event management.
func NewOutboxMessageEvent() *outboxMessageEvent {
	outboxOnce.Do(func() {
		conf := config.NewConfigLoader()
		cli, _, err := conf.RedisConfig.GetClient()
		if err != nil {
			panic(fmt.Sprintf("get redis client failed: %v", err))
		}
		outboxEvent = &outboxMessageEvent{
			confLoader:      conf,
			logger:          conf.GetLogger(),
			outboxMessageDB: dbaccess.NewOutboxMessageDB(),
			mqClient:        mq.NewMQClient(),
			redisCli:        cli,
			quit:            make(chan bool),
		}
	})
	return outboxEvent
}

// Start starts outboxMessageEvent.
func (m *outboxMessageEvent) Start() error {
	m.logger.Info("[outboxMessageEvent] start scan outbox message event")
	go func() {
		ticker := time.NewTicker(commonPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				m.scan(context.Background())
			case <-m.quit:
				m.logger.Info("[outboxMessageEvent] stop scan outbox message event")
				return
			}
		}
	}()
	return nil
}
func (m *outboxMessageEvent) scan(ctx context.Context) {
	var err error
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	// Get distributed lock.
	v := m.confLoader.Project.GetMachineID()
	locker := lock.NewRedisLocker(m.redisCli, lockKeyTemp, v, defultLockExpiryTime)
	ok, err := locker.Lock(ctx)
	if err != nil && err != redis.Nil {
		m.logger.WithContext(ctx).Warnf("[auditLogHandler] processFaildLogData get lock err: %s", err.Error())
		return
	}
	if !ok {
		return
	}
	defer locker.Unlock(ctx)
	// Get unprocessed messages.
	events, err := m.outboxMessageDB.GetByStatus(ctx, model.OutboxMessageStatusPending, queryDefaultLimit)
	if err != nil {
		m.logger.WithContext(ctx).Errorf("[auditLogHandler] processFaildLogData get outbox message err: %s", err.Error())
		return
	}
	// Handle message events.
	for _, event := range events {
		m.processOutboxEventMessage(ctx, event)
	}
}

func (m *outboxMessageEvent) processOutboxEventMessage(ctx context.Context, event *model.OutboxMessageDB) {
	// Send message to MQ.
	err := m.mqClient.Publish(ctx, event.Topic, []byte(event.Payload))
	if err == nil {
		// Clean up messages.
		err = m.outboxMessageDB.DeleteByEventID(ctx, nil, event.EventID)
		if err != nil {
			m.logger.WithContext(ctx).Errorf("delete outbox message failed: %v, topic:%s, message:%s", err, event.Topic, event.Payload)
		}
		return
	}
	m.logger.WithContext(ctx).Warnf("publish outbox message failed: %v, topic:%s, message:%s", err, event.Topic, event.Payload)
	event.RetryCount++
	event.NextRetryAt = time.Now().Add(time.Duration(event.RetryCount) * commonPollInterval).UnixNano()
	err = m.outboxMessageDB.UpdateByEventID(ctx, nil, event)
	if err != nil {
		m.logger.WithContext(ctx).Errorf("update outbox message failed: %v, topic:%s, message:%s", err, event.Topic, event.Payload)
	}
}

// Stop Stop outboxMessageEvent.
func (m *outboxMessageEvent) Stop(ctx context.Context) {
	close(m.quit)
}

// Publish publish message event.
func (m *outboxMessageEvent) Publish(ctx context.Context, req *interfaces.OutboxMessageReq) (err error) {
	// Parameter verification.
	err = defaults.Set(req)
	if err != nil {
		return
	}
	err = validator.New().Struct(req)
	if err != nil {
		return
	}
	// Send message to MQ.
	err = m.mqClient.Publish(ctx, req.Topic, []byte(req.Payload))
	if err == nil {
		return
	}
	m.logger.WithContext(ctx).Warnf("publish outbox message failed: %v, topic:%s, message:%s", err, req.Topic, req.Payload)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), defaultTimeout)
		defer cancel()
	}
	// Processing failed, save message to database.
	event := &model.OutboxMessageDB{
		EventID:     req.EventID,
		EventType:   req.EventType.String(),
		Topic:       req.Topic,
		Payload:     req.Payload,
		NextRetryAt: time.Now().Add(commonPollInterval).UnixNano(),
		Status:      model.OutboxMessageStatusPending,
	}
	// Save message to database.
	_, err = m.outboxMessageDB.Insert(ctx, nil, event)
	if err != nil {
		m.logger.WithContext(ctx).Errorf("insert outbox message failed: %v", err)
	}
	return
}
