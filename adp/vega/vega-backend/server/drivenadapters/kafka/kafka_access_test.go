// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package kafka

import (
	"context"
	"testing"
	"time"

	libmq "github.com/openbkn-ai/bkn-comm-go/mq"
	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl/plain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/common"
	"vega-backend/interfaces"
)

func TestKafkaAccessOptions(t *testing.T) {
	t.Run("broker address", func(t *testing.T) {
		access := newKafkaAccess("kafka.local", 9092, libmq.MQAuthSetting{})

		assert.Equal(t, "kafka.local:9092", access.getBrokerAddress())
	})

	t.Run("dialer omits sasl without credentials", func(t *testing.T) {
		access := newKafkaAccess("kafka.local", 9092, libmq.MQAuthSetting{})

		dialer := access.getSASLDialer()

		assert.Equal(t, 10*time.Second, dialer.Timeout)
		assert.True(t, dialer.DualStack)
		assert.Nil(t, dialer.SASLMechanism)
	})

	t.Run("plain sasl", func(t *testing.T) {
		access := newKafkaAccess("kafka.local", 9092, libmq.MQAuthSetting{
			Username:  "user",
			Password:  "pass",
			Mechanism: "PLAIN",
		})

		mechanism := access.getSASLMechanism()
		_, ok := mechanism.(plain.Mechanism)

		require.True(t, ok)
		assert.Equal(t, "PLAIN", mechanism.Name())
		assert.NotNil(t, access.getSASLDialer().SASLMechanism)
	})

	t.Run("scram mechanisms", func(t *testing.T) {
		for _, mechanismName := range []string{"SCRAM-SHA-256", "SCRAM-SHA-512"} {
			access := newKafkaAccess("kafka.local", 9092, libmq.MQAuthSetting{
				Username:  "user",
				Password:  "pass",
				Mechanism: mechanismName,
			})

			mechanism := access.getSASLMechanism()

			require.NotNil(t, mechanism)
			assert.Equal(t, mechanismName, mechanism.Name())
		}
	})

	t.Run("unknown mechanism falls back to plain", func(t *testing.T) {
		access := newKafkaAccess("kafka.local", 9092, libmq.MQAuthSetting{
			Username:  "user",
			Password:  "pass",
			Mechanism: "unknown",
		})

		assert.Equal(t, "PLAIN", access.getSASLMechanism().Name())
	})
}

func TestKafkaAccessReaderWriterConstruction(t *testing.T) {
	access := newKafkaAccess("kafka.local", 9092, libmq.MQAuthSetting{
		Username:  "user",
		Password:  "pass",
		Mechanism: "PLAIN",
	})

	reader, err := access.NewReader(context.Background(), "topic-a", "group-a")
	require.NoError(t, err)
	require.NotNil(t, reader)

	writer, err := access.NewWriter(context.Background(), "topic-a")
	require.NoError(t, err)
	require.NotNil(t, writer)
	defer access.CloseWriter(writer)
	assert.Equal(t, "topic-a", writer.Topic)
	assert.Equal(t, 1, writer.BatchSize)
	assert.Equal(t, 10*time.Millisecond, writer.BatchTimeout)
	assert.Equal(t, 10*time.Second, writer.WriteTimeout)
	assert.Equal(t, 10*time.Second, writer.ReadTimeout)
	assert.Equal(t, kafka.RequireAll, writer.RequiredAcks)
	assert.NotNil(t, writer.Transport)
}

func TestKafkaAccessReaderWriterWithoutAuth(t *testing.T) {
	access := newKafkaAccess("kafka.local", 9092, libmq.MQAuthSetting{})

	reader, err := access.NewReader(context.Background(), "topic-a", "group-a")
	require.NoError(t, err)
	require.NotNil(t, reader)
	access.CloseReader(reader)

	writer, err := access.NewWriter(context.Background(), "topic-a")
	require.NoError(t, err)
	require.NotNil(t, writer)
	defer access.CloseWriter(writer)
	assert.Nil(t, writer.Transport)
}

func TestKafkaAccessEmptyAndNilOperations(t *testing.T) {
	access := newKafkaAccess("kafka.local", 9092, libmq.MQAuthSetting{})

	require.NoError(t, access.WriteMessages(context.Background(), nil))
	access.CloseReader(nil)
	access.CloseWriter(nil)
}

func TestKafkaAccessCreateTopicDialError(t *testing.T) {
	access := newKafkaAccess("127.0.0.1", 1, libmq.MQAuthSetting{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := access.CreateTopic(ctx, "topic-a")

	require.Error(t, err)
}

func newKafkaAccess(host string, port int, auth libmq.MQAuthSetting) *kafkaAccess {
	return &kafkaAccess{appSetting: &common.AppSetting{
		MQSetting: libmq.MQSetting{
			MQHost: host,
			MQPort: port,
			Auth:   auth,
		},
	}}
}

func TestKafkaConstantsMatchWriterDefaults(t *testing.T) {
	assert.Equal(t, 20971520, interfaces.MAX_MESSAGE_BYTES)
}
