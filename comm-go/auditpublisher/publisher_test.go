package auditpublisher

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"sync"
	"testing"
	"time"
)

const goldenFixtureSHA256 = "2976cc4822bc9a9248b1aa66de29916a35fcb9988b61a313d6e86fc68c17ce40"

func TestRecordContract(t *testing.T) {
	value, err := os.ReadFile("testdata/audit-record-v1-golden.json")
	if err != nil {
		t.Fatal(err)
	}
	fixtureDigest := sha256.Sum256(value)
	if actual := hex.EncodeToString(fixtureDigest[:]); actual != goldenFixtureSHA256 {
		t.Fatalf("fixture digest = %s", actual)
	}

	record, err := BuildRecord(value)
	if err != nil {
		t.Fatal(err)
	}
	if record.Topic != Topic {
		t.Fatalf("topic = %q", record.Topic)
	}
	wantKey := []byte("bkn-backend\x1fknowledge_network\x1fkn-123")
	if !bytes.Equal(record.Key, wantKey) {
		t.Fatalf("key = %q", record.Key)
	}
	if record.TimestampType != LogAppendTime {
		t.Fatalf("timestamp type = %q", record.TimestampType)
	}
	if len(record.Headers) != 1 || record.Headers[0].Key != SchemaVersionHeader || !bytes.Equal(record.Headers[0].Value, []byte("1.0")) {
		t.Fatalf("headers = %#v", record.Headers)
	}
	const wantHash = "sha256:6c1e715bff7d73ef22bf751db3bb5e89cada868eca1741badcef00c5b9ff178f"
	if record.ContentHash != wantHash {
		t.Fatalf("content hash = %q", record.ContentHash)
	}
}

func TestRejectsOversizeOrSecret(t *testing.T) {
	value, err := os.ReadFile("testdata/audit-record-v1-golden.json")
	if err != nil {
		t.Fatal(err)
	}
	oversize := append(append([]byte(nil), value...), bytes.Repeat([]byte(" "), MaxValueBytes+1)...)
	if _, err := BuildRecord(oversize); err == nil {
		t.Fatal("oversize value was accepted")
	}
	secret := bytes.Replace(value, []byte(`"summary": "updated knowledge network"`), []byte(`"summary": "Bearer abcdefghijklmnop"`), 1)
	if _, err := BuildRecord(secret); err == nil {
		t.Fatal("secret-shaped value was accepted")
	}
}

type fakeSender struct {
	mu       sync.Mutex
	calls    int
	failures int
	block    bool
}

func (s *fakeSender) Send(ctx context.Context, _ Record) error {
	s.mu.Lock()
	s.calls++
	call := s.calls
	s.mu.Unlock()
	if s.block {
		<-ctx.Done()
		return ctx.Err()
	}
	if call <= s.failures {
		return bytes.ErrTooLarge
	}
	return nil
}

type deliveryObserver struct {
	mu         sync.Mutex
	deliveries []Delivery
}

func (o *deliveryObserver) ObserveDelivery(d Delivery) {
	o.mu.Lock()
	o.deliveries = append(o.deliveries, d)
	o.mu.Unlock()
}

func fixtureValue(t *testing.T) []byte {
	t.Helper()
	value, err := os.ReadFile("testdata/audit-record-v1-golden.json")
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func testConfig() publisherConfig {
	cfg := defaultPublisherConfig()
	cfg.QueueMaxRecords = 1
	cfg.QueueMaxBytes = 1 << 20
	cfg.Workers = 1
	cfg.Linger = 0
	cfg.MaxElapsed = 20 * time.Millisecond
	cfg.ShutdownTimeout = 20 * time.Millisecond
	return cfg
}

func TestTryPublishIsBoundedAndNonBlocking(t *testing.T) {
	sender := &fakeSender{block: true}
	p, err := newPublisher(sender, nil, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	value := fixtureValue(t)
	if got := p.TryPublish(value); got != Accepted {
		t.Fatalf("first disposition = %q", got)
	}
	if got := p.TryPublish(value); got != DroppedQueueFull {
		t.Fatalf("second disposition = %q", got)
	}
	if got := p.TryPublish([]byte(`{"schema_version":"1.0"}`)); got != DroppedInvalid {
		t.Fatalf("invalid disposition = %q", got)
	}
	p.Close()
	if got := p.TryPublish(value); got != DroppedClosed {
		t.Fatalf("closed disposition = %q", got)
	}
}

func TestRetriesAreFiniteAndObserved(t *testing.T) {
	sender := &fakeSender{failures: 10}
	observer := &deliveryObserver{}
	cfg := testConfig()
	cfg.MaxElapsed = time.Second
	p, err := newPublisher(sender, observer, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.TryPublish(fixtureValue(t)); got != Accepted {
		t.Fatal("record was not accepted")
	}
	p.Close()
	if sender.calls != cfg.MaxAttempts {
		t.Fatalf("sender calls = %d, want %d", sender.calls, cfg.MaxAttempts)
	}
	if len(observer.deliveries) != 1 || observer.deliveries[0].Outcome != RetryExhausted {
		t.Fatalf("deliveries = %#v", observer.deliveries)
	}
}
