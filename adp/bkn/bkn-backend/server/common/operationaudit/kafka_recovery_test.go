// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the repository root.

package operationaudit

import (
	"context"
	"errors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/auditpublisher"
	"sync/atomic"
	"testing"
	"time"
)

type recoveryPublisher struct{ calls atomic.Int32 }

func (p *recoveryPublisher) TryPublish([]byte) auditpublisher.Disposition {
	p.calls.Add(1)
	return auditpublisher.Accepted
}

func TestPublisherRecoversAfterBootFailureAndClosesOnStop(t *testing.T) {
	recorder := NewKafkaRecorder(nil, "test")
	entry := Entry{EventID: "evt-1", EventTime: time.Now().UTC(), KnowledgeNetworkID: "kn-1", ActorID: "user-a", ActorName: "User A", ActorType: "user", AuthMethod: "oauth", RequestID: "req-1", SourceChannel: "api", Method: "PUT", HTTPStatus: 200, Action: "update", TargetType: "knowledge_network", TargetID: "kn-1", TargetName: "Supply chain", Outcome: "success", ChangeSummary: map[string]any{"changed_fields": []string{"name"}}}
	if err := recorder.Record(context.Background(), entry); err == nil {
		t.Fatal("initial failure must report a coverage gap")
	}
	stop, done, ticks := make(chan struct{}), make(chan struct{}), make(chan time.Time)
	publisher := &recoveryPublisher{}
	var attempts, closes atomic.Int32
	connected := make(chan struct{})
	go func() {
		defer close(done)
		ReconnectPublisher(stop, ticks, recorder, func() (KafkaPublisher, func(), error) {
			if attempts.Add(1) == 1 {
				return nil, nil, errors.New("broker unavailable")
			}
			close(connected)
			return publisher, func() { closes.Add(1) }, nil
		})
	}()
	ticks <- time.Now()
	ticks <- time.Now()
	<-connected
	deadline := time.Now().Add(time.Second)
	for recorder.Record(context.Background(), entry) != nil {
		if time.Now().After(deadline) {
			t.Fatal("publisher never recovered")
		}
	}
	if attempts.Load() != 2 || publisher.calls.Load() != 1 || closes.Load() != 0 {
		t.Fatal("unexpected recovery state")
	}
	close(stop)
	<-done
	if closes.Load() != 1 || recorder.Record(context.Background(), entry) == nil {
		t.Fatal("publisher shutdown ownership violated")
	}
}

func TestPublisherRecoveryStopDuringConnectClosesWithoutAttachment(t *testing.T) {
	stop, ticks := make(chan struct{}), make(chan time.Time, 1)
	ticks <- time.Now()
	recorder := NewKafkaRecorder(nil, "test")
	var closes int
	ReconnectPublisher(stop, ticks, recorder, func() (KafkaPublisher, func(), error) {
		close(stop)
		return &recoveryPublisher{}, func() { closes++ }, nil
	})
	if closes != 1 {
		t.Fatalf("close count=%d", closes)
	}
	recorder.mu.RLock()
	defer recorder.mu.RUnlock()
	if recorder.publisher != nil {
		t.Fatal("stopped recovery attached a publisher")
	}
}

func TestKafkaRecorderConcurrentPublisherReplacement(t *testing.T) {
	recorder := NewKafkaRecorder(nil, "test")
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 1000 {
			_ = recorder.Record(context.Background(), Entry{})
		}
	}()
	for range 1000 {
		recorder.SetPublisher(&recoveryPublisher{})
		recorder.SetPublisher(nil)
	}
	<-done
}
