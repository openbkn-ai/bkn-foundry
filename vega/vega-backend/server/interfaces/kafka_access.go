// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"context"

	"github.com/segmentio/kafka-go"
)

const (
	// poll blocking time
	POLL_TIMEOUT_MS = 100
	// The time when the producer blocks the production of messages
	PRODUCE_FLUSH_TIMEOUT_MS = 100

	// Set it to 5 to balance throughput and sequentiality
	MAX_IN_FLIGHT_REQUESTS_PER_CONNECTION = 5

	// The maximum length of the topic is limited to 249-len("<tenant>.mdl.dr.<clusterID>.customer."). The maximum length of the tenant is 10 characters. kafka itself limits the length to 249 characters, and the clusterID to 22 characters
	SRC_TOPIC_MAX_LENGTH = 200

	// The maximum allowed message size (byte)
	MAX_MESSAGE_BYTES = 20971520

	// The retention time of kafka messages is 8 hours
	RETENTION_MS = "28800000"
	// The retention size of a single kafka message partition is 100M
	RETENTION_BYTES = "104857600"
)

//go:generate mockgen -source ../interfaces/kafka_access.go -destination ../interfaces/mock/mock_kafka_access.go
type KafkaAccess interface {
	NewReader(ctx context.Context, topic string, groupID string) (*kafka.Reader, error)
	NewWriter(ctx context.Context, topic string) (*kafka.Writer, error)
	WriteMessages(ctx context.Context, w *kafka.Writer, msgs ...kafka.Message) error
	ReadMessage(ctx context.Context, r *kafka.Reader) (kafka.Message, error)
	CommitMessages(ctx context.Context, r *kafka.Reader, msgs ...kafka.Message) error
	CreateTopic(ctx context.Context, topicName string) error
	CloseReader(r *kafka.Reader)
	CloseWriter(w *kafka.Writer)
}
