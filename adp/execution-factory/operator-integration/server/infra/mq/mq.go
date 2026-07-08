// Package mq MQ客户端
package mq

import (
	"context"
	"fmt"
	"sync"

	msqclient "github.com/openbkn-ai/bkn-comm-go/mq"
	"github.com/openbkn-ai/bkn-comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/attribute"

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
)

//go:generate mockgen -package mock -source ./mq.go -destination ./mock/mock_mq.go

// MQClient mq客户端接口
type MQClient interface {
	Subscribe(topic string, channel string, cmd func(context.Context, []byte) error)
	Publish(ctx context.Context, topic string, message []byte) error
}

var (
	mqOnce   sync.Once
	mqClient MQClient
)

type msgQueue struct {
	logger                   interfaces.Logger
	openBKNMQClient          msqclient.OpenBKNMQClient
	pollIntervalMilliseconds int64
	maxInFlight              int
}

// NewMQClient 创建消息队列
func NewMQClient() MQClient {
	mqOnce.Do(func() {
		configLoader := config.NewConfigLoader()
		openBKNClient, err := msqclient.NewOpenBKNMQClientFromFile(configLoader.MQConfigFile)
		if err != nil {
			panic(err)
		}
		mqClient = &msgQueue{
			logger:                   configLoader.GetLogger(),
			openBKNMQClient:          openBKNClient,
			pollIntervalMilliseconds: 100, //nolint:mnd
			maxInFlight:              200, //nolint:mnd
		}
	})
	return mqClient
}

// Subscribe 订阅
func (m *msgQueue) Subscribe(topic, channel string, cmd func(context.Context, []byte) error) {
	go func() {
		var err error
		ctx := context.Background()
		ctx, span := oteltrace.StartNamedConsumerSpan(ctx, "mq.subscribe")
		span.SetAttributes(attribute.String("messaging.operation", "subscribe"))
		span.SetAttributes(attribute.String("messaging.topic", topic))
		span.SetAttributes(attribute.String("messaging.channel", channel))
		defer oteltrace.EndSpan(ctx, err)

		err = m.openBKNMQClient.Sub(topic, channel, func(msg []byte) error {
			return cmd(ctx, msg)
		}, m.pollIntervalMilliseconds, m.maxInFlight)
		m.logger.WithContext(ctx).Errorf("subscribe mq topic: %s, channel: %s,  error: %v", topic, channel, err)
	}()
}

// Publish 发布
func (m *msgQueue) Publish(ctx context.Context, topic string, message []byte) (err error) {
	ctx, span := oteltrace.StartNamedProducerSpan(ctx, "mq.publish")
	span.SetAttributes(attribute.String("messaging.operation", "publish"))
	span.SetAttributes(attribute.String("messaging.topic", topic))
	span.SetAttributes(attribute.String("messaging.payload_size_bytes", fmt.Sprintf("%d", int64(len(message)))))
	defer oteltrace.EndSpan(ctx, err)

	if err := m.openBKNMQClient.Pub(topic, message); err != nil {
		m.logger.WithContext(ctx).Errorf("publish mq topic %s, message: %s, error: %v", topic, string(message), err)
		return err
	}
	return nil
}
