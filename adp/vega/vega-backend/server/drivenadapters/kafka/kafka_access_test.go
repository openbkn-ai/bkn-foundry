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

func TestNewKafkaAccess(t *testing.T) {
	t.Run("returns singleton access", func(t *testing.T) {
		access1 := NewKafkaAccess(newKafkaAppSetting("kafka-a.local", 9092, libmq.MQAuthSetting{}))
		access2 := NewKafkaAccess(newKafkaAppSetting("kafka-b.local", 9093, libmq.MQAuthSetting{}))

		require.NotNil(t, access1)
		assert.Same(t, access1, access2)
	})
}

func TestKafkaAccessGetSASLMechanism(t *testing.T) {
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

func TestKafkaAccessGetSASLDialer(t *testing.T) {
	t.Run("omits sasl without credentials", func(t *testing.T) {
		access := newKafkaAccess("kafka.local", 9092, libmq.MQAuthSetting{})

		dialer := access.getSASLDialer()

		assert.Equal(t, 10*time.Second, dialer.Timeout)
		assert.True(t, dialer.DualStack)
		assert.Nil(t, dialer.SASLMechanism)
	})

	t.Run("uses sasl with credentials", func(t *testing.T) {
		access := newKafkaAccess("kafka.local", 9092, libmq.MQAuthSetting{
			Username:  "user",
			Password:  "pass",
			Mechanism: "PLAIN",
		})

		dialer := access.getSASLDialer()

		assert.NotNil(t, dialer.SASLMechanism)
	})
}

func TestKafkaAccessGetBrokerAddress(t *testing.T) {
	t.Run("formats host and port", func(t *testing.T) {
		access := newKafkaAccess("kafka.local", 9092, libmq.MQAuthSetting{})

		assert.Equal(t, "kafka.local:9092", access.getBrokerAddress())
	})
}

func TestKafkaAccessNewReader(t *testing.T) {
	t.Run("creates reader", func(t *testing.T) {
		access := newKafkaAccess("kafka.local", 9092, libmq.MQAuthSetting{})

		reader, err := access.NewReader(context.Background(), "topic-a", "group-a")
		t.Cleanup(func() { access.CloseReader(reader) })

		require.NoError(t, err)
		require.NotNil(t, reader)
	})
}

func TestKafkaAccessCloseReader(t *testing.T) {
	t.Run("ignores nil reader", func(t *testing.T) {
		access := newKafkaAccess("kafka.local", 9092, libmq.MQAuthSetting{})

		access.CloseReader(nil)
	})

	t.Run("closes reader", func(t *testing.T) {
		access := newKafkaAccess("kafka.local", 9092, libmq.MQAuthSetting{})
		reader, err := access.NewReader(context.Background(), "topic-a", "group-a")
		require.NoError(t, err)

		access.CloseReader(reader)
	})
}

func TestKafkaAccessNewWriter(t *testing.T) {
	t.Run("creates writer with sasl", func(t *testing.T) {
		access := newKafkaAccess("kafka.local", 9092, libmq.MQAuthSetting{
			Username:  "user",
			Password:  "pass",
			Mechanism: "PLAIN",
		})

		writer, err := access.NewWriter(context.Background(), "topic-a")
		t.Cleanup(func() { access.CloseWriter(writer) })

		require.NoError(t, err)
		require.NotNil(t, writer)
		assert.Equal(t, "topic-a", writer.Topic)
		assert.Equal(t, 1, writer.BatchSize)
		assert.Equal(t, 10*time.Millisecond, writer.BatchTimeout)
		assert.Equal(t, 10*time.Second, writer.WriteTimeout)
		assert.Equal(t, 10*time.Second, writer.ReadTimeout)
		assert.Equal(t, kafka.RequireAll, writer.RequiredAcks)
		assert.NotNil(t, writer.Transport)
	})

	t.Run("creates writer without auth", func(t *testing.T) {
		access := newKafkaAccess("kafka.local", 9092, libmq.MQAuthSetting{})

		writer, err := access.NewWriter(context.Background(), "topic-a")
		t.Cleanup(func() { access.CloseWriter(writer) })

		require.NoError(t, err)
		require.NotNil(t, writer)
		assert.Nil(t, writer.Transport)
	})
}

func TestKafkaAccessCloseWriter(t *testing.T) {
	t.Run("ignores nil writer", func(t *testing.T) {
		access := newKafkaAccess("kafka.local", 9092, libmq.MQAuthSetting{})

		access.CloseWriter(nil)
	})
}

func TestKafkaAccessWriteMessages(t *testing.T) {
	t.Run("skips empty messages", func(t *testing.T) {
		access := newKafkaAccess("kafka.local", 9092, libmq.MQAuthSetting{})

		require.NoError(t, access.WriteMessages(context.Background(), nil))
	})
}

func TestKafkaAccessCreateTopic(t *testing.T) {
	t.Run("returns dial error", func(t *testing.T) {
		access := newKafkaAccess("127.0.0.1", 1, libmq.MQAuthSetting{})
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		err := access.CreateTopic(ctx, "topic-a")

		require.Error(t, err)
	})
}

func newKafkaAccess(host string, port int, auth libmq.MQAuthSetting) *kafkaAccess {
	return &kafkaAccess{appSetting: newKafkaAppSetting(host, port, auth)}
}

func newKafkaAppSetting(host string, port int, auth libmq.MQAuthSetting) *common.AppSetting {
	return &common.AppSetting{
		MQSetting: libmq.MQSetting{
			MQHost: host,
			MQPort: port,
			Auth:   auth,
		},
	}
}

func TestKafkaConstantsMatchWriterDefaults(t *testing.T) {
	t.Run("max message bytes", func(t *testing.T) {
		assert.Equal(t, 20971520, interfaces.MAX_MESSAGE_BYTES)
	})
}
