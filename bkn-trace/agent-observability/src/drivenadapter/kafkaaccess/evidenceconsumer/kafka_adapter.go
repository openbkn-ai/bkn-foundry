package evidenceconsumer

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/segmentio/kafka-go"
)

// KafkaProcessor adapts kafka-go transport data to the durable Evidence
// processor. A non-nil error deliberately prevents kafkaruntime from
// committing the offset.
func KafkaProcessor(processor *Processor) func(context.Context, kafka.Message) error {
	return func(ctx context.Context, message kafka.Message) error {
		if processor == nil {
			return fmt.Errorf("evidence processor is nil")
		}
		var value struct {
			ProducerStreamID string `json:"producer_stream_id"`
			ProducerSequence uint64 `json:"producer_sequence"`
		}
		if err := json.Unmarshal(message.Value, &value); err != nil {
			return fmt.Errorf("decode Evidence transport identity: %w", err)
		}
		record := Record{Topic: message.Topic, Key: string(message.Key), Value: append([]byte(nil), message.Value...), Partition: message.Partition, Offset: message.Offset, BrokerTime: message.Time.UTC(), BrokerTimestamp: message.Time.UTC().Format(time.RFC3339Nano), ProducerStreamID: value.ProducerStreamID, ProducerSequence: value.ProducerSequence}
		for _, header := range message.Headers {
			record.Headers = append(record.Headers, Header{Key: header.Key, Value: string(header.Value)})
		}
		return processor.Process(ctx, record)
	}
}
