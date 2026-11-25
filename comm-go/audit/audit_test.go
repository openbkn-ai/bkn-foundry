// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package audit

import (
	"encoding/json"
	"testing"
	"uuid"

	"github.com/IBM/sarama"
)

type recordingProducer struct {
	sarama.SyncProducer
	message *sarama.ProducerMessage
}

func (p *recordingProducer) SendMessage(msg *sarama.ProducerMessage) (int32, int64, error) {
	p.message = msg
	return 0, 0, nil
}

func TestTransformLogGeneratesUUIDV7(t *testing.T) {
	auditLog := &AuditLog{Detail: map[string]string{}}
	transformLog(auditLog)

	id, err := uuid.Parse(auditLog.ID)
	if err != nil {
		t.Fatalf("audit log ID is not a UUID: %v", err)
	}
	if id[6]>>4 != 7 || id.String() != auditLog.ID {
		t.Fatalf("audit log ID = %q, want a canonical UUID v7", auditLog.ID)
	}
}

func TestSendLogJSON(t *testing.T) {
	producer := &recordingProducer{}
	previous := auditProducer
	auditProducer = producer
	t.Cleanup(func() { auditProducer = previous })
	sendLog(&AuditLog{Type: "operation", ID: "test-id", Status: "success", Detail: map[string]string{"detail": "<测试>&"}})
	if producer.message == nil || producer.message.Topic != AUDIT_TOPIC {
		t.Fatalf("unexpected message: %+v", producer.message)
	}
	encoded, err := producer.message.Value.Encode()
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["type"] != "operation" || decoded["out_biz_id"] != "test-id" || decoded["detail"].(map[string]any)["detail"] != "<测试>&" {
		t.Fatalf("unexpected audit data: %s", encoded)
	}
	if _, ok := decoded["Status"]; ok {
		t.Fatalf("status should not be serialized: %s", encoded)
	}
}
