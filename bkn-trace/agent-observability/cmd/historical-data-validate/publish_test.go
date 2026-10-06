// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/segmentio/kafka-go"
	"net"
	"os"
	"strings"
	"testing"
)

type fakePublisher struct {
	calls  int
	fail   bool
	values [][]byte
}

func (p *fakePublisher) Publish(_ context.Context, payload []byte) (publicationReceipt, error) {
	p.calls++
	p.values = append(p.values, append([]byte(nil), payload...))
	if p.fail {
		return publicationReceipt{}, errors.New("secret unreachable broker")
	}
	return publicationReceipt{Topic: "openbkn.audit.v1", Partition: 2, Offset: int64(p.calls)}, nil
}
func (p *fakePublisher) Close() error { return nil }

func publishFixture(t *testing.T) []byte {
	t.Helper()
	value, err := os.ReadFile("../../src/drivenadapter/kafkaaccess/auditvalidator/assets/execution-factory-golden.json")
	if err != nil {
		t.Fatal(err)
	}
	request, err := json.Marshal(input{Kind: "audit", Payload: value, BrokerTime: "2026-09-24T08:31:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	return append(request, '\n')
}
func planDigest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func TestPublishRequiresMatchingDigestBeforeFactory(t *testing.T) {
	data := publishFixture(t)
	for _, digest := range []string{"", "bad", planDigest(append(data, ' '))} {
		called := false
		var output bytes.Buffer
		err := publishAudit(bytes.NewReader(data), &output, digest, func() (auditPublisher, error) { called = true; return &fakePublisher{}, nil })
		if err == nil || called {
			t.Fatal("unapproved plan opened publisher")
		}
	}
}
func TestPublishNativeValidatedPayloadAndCoordinates(t *testing.T) {
	data := publishFixture(t)
	publisher := &fakePublisher{}
	var output bytes.Buffer
	err := publishAudit(bytes.NewReader(data), &output, planDigest(data), func() (auditPublisher, error) { return publisher, nil })
	if err != nil {
		t.Fatal(err)
	}
	var receipt result
	if err := json.Unmarshal(output.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if publisher.calls != 1 || !receipt.Accepted || receipt.Kafka == nil || receipt.Kafka.Partition != 2 || receipt.Kafka.Offset != 1 || len(receipt.CanonicalPayload) != 0 {
		t.Fatalf("unexpected receipt: %s", output.String())
	}
	var candidate input
	_ = json.Unmarshal(data, &candidate)
	if !bytes.Equal(publisher.values[0], validate(candidate).CanonicalPayload) {
		t.Fatal("changed validated payload")
	}
}
func TestPublishRejectsWholeInvalidPlan(t *testing.T) {
	data := append(publishFixture(t), []byte("{\"kind\":\"evidence\",\"payload\":{}}\n")...)
	called := false
	var output bytes.Buffer
	err := publishAudit(bytes.NewReader(data), &output, planDigest(data), func() (auditPublisher, error) { called = true; return &fakePublisher{}, nil })
	if err == nil || called || bytes.Count(output.Bytes(), []byte("\n")) != 2 {
		t.Fatal("invalid plan not safely rejected")
	}
}
func TestPublishFailureStopsAndDoesNotLeak(t *testing.T) {
	data := append(publishFixture(t), publishFixture(t)...)
	publisher := &fakePublisher{fail: true}
	var output bytes.Buffer
	err := publishAudit(bytes.NewReader(data), &output, planDigest(data), func() (auditPublisher, error) { return publisher, nil })
	if err == nil || publisher.calls != 1 || bytes.Contains(output.Bytes(), []byte("secret")) || bytes.Count(output.Bytes(), []byte("\n")) != 2 {
		t.Fatalf("unsafe failure: %s", output.String())
	}
}

func TestKafkaPublisherRefusesMissingAuthentication(t *testing.T) {
	for _, key := range []string{"BKN_HISTORY_KAFKA_BROKERS", "BKN_HISTORY_KAFKA_USERNAME", "BKN_HISTORY_KAFKA_PASSWORD", "BKN_HISTORY_KAFKA_MECHANISM"} {
		t.Setenv("BKN_HISTORY_KAFKA_BROKERS", "unreachable.invalid:9092")
		t.Setenv("BKN_HISTORY_KAFKA_USERNAME", "user")
		t.Setenv("BKN_HISTORY_KAFKA_PASSWORD", "do-not-log")
		t.Setenv("BKN_HISTORY_KAFKA_MECHANISM", "PLAIN")
		t.Setenv(key, "")
		if _, err := newKafkaPublisher(); err == nil {
			t.Fatalf("accepted missing %s", key)
		}
	}
	t.Setenv("BKN_HISTORY_KAFKA_BROKERS", "unreachable.invalid:9092")
	t.Setenv("BKN_HISTORY_KAFKA_MECHANISM", "NONE")
	if _, err := newKafkaPublisher(); err == nil {
		t.Fatal("accepted unauthenticated mechanism")
	}
}

func TestPublishRequiresQualificationFlag(t *testing.T) {
	var output bytes.Buffer
	if runCommand([]string{"--publish-audit", "--expected-plan-sha256", planDigest(publishFixture(t))}, bytes.NewReader(publishFixture(t)), &output) == nil {
		t.Fatal("release publishing enabled without qualification")
	}
}

func TestInPlaceUpgradeUsesCurrentAuditServiceConfiguration(t *testing.T) {
	t.Setenv("BKN_TRACE_AUDIT_KAFKA_ENABLED", "true")
	t.Setenv("BKN_TRACE_AUDIT_KAFKA_BROKERS", "kafka.resource.svc.cluster.local:9092")
	t.Setenv("BKN_TRACE_AUDIT_KAFKA_SASL_MECHANISM", "PLAIN")
	t.Setenv("BKN_TRACE_AUDIT_KAFKA_USERNAME", "migration-source")
	t.Setenv("BKN_TRACE_AUDIT_KAFKA_PASSWORD", "private")
	t.Setenv("BKN_TRACE_AUDIT_KAFKA_GROUP", "audit-writer")
	configuration, err := inPlaceAuditConfig()
	if err != nil || configuration.Topic != "openbkn.audit.v1" || configuration.Brokers[0] != "kafka.resource.svc.cluster.local:9092" {
		t.Fatal("existing service configuration not reused")
	}
	t.Setenv("BKN_TRACE_AUDIT_KAFKA_ENABLED", "false")
	if _, err := inPlaceAuditConfig(); err == nil {
		t.Fatal("disabled Audit service accepted")
	}
}

func TestPublishRejectsFalseHistoricalBrokerClock(t *testing.T) {
	payload, err := os.ReadFile("../../src/drivenadapter/kafkaaccess/auditvalidator/assets/execution-factory-golden.json")
	if err != nil {
		t.Fatal(err)
	}
	payload = bytes.Replace(payload, []byte("2026-09-24T08:30:00Z"), []byte("2000-01-01T00:00:00Z"), 1)
	request, _ := json.Marshal(input{Kind: "audit", Payload: payload, BrokerTime: "2000-01-01T00:01:00Z"})
	data := append(request, '\n')
	called := false
	var output bytes.Buffer
	err = publishAudit(bytes.NewReader(data), &output, planDigest(data), func() (auditPublisher, error) { called = true; return &fakePublisher{}, nil })
	if err == nil || called || !bytes.Contains(output.Bytes(), []byte("retention_expired")) {
		t.Fatal("publishing bypassed actual retention clock")
	}
}

func TestQualificationAddressesAndAdvertisedBrokers(t *testing.T) {
	for _, address := range []string{"127.0.0.1:9092", "localhost:9092", "[::1]:9092"} {
		if !qualificationAddress(address) {
			t.Fatalf("loopback rejected: %s", address)
		}
	}
	for _, address := range []string{"broker.internal:9092", "192.168.1.1:9092", "127.0.0.2:9092", "0.0.0.0:9092", "localhost:0", "localhost:65536", "localhost"} {
		if qualificationAddress(address) {
			t.Fatalf("external/invalid accepted: %s", address)
		}
	}
	resolver := qualificationResolver{}
	if _, err := resolver.LookupBrokerIPAddr(context.Background(), kafka.Broker{Host: "broker.internal", Port: 9092}); err == nil {
		t.Fatal("advertised external broker accepted")
	}
	ips, err := resolver.LookupBrokerIPAddr(context.Background(), kafka.Broker{Host: "localhost", Port: 9092})
	if err != nil || len(ips) != 1 || !ips[0].IP.Equal(net.ParseIP("127.0.0.1")) {
		t.Fatal("localhost resolution not pinned")
	}
	if _, err := qualificationDial(context.Background(), "tcp", "192.168.1.1:9092"); err == nil {
		t.Fatal("external dial accepted")
	}
}

func TestPublishStrictWrapperBeforeFactory(t *testing.T) {
	good := string(publishFixture(t))
	for _, data := range []string{
		strings.Replace(good, `"kind":"audit"`, `"kind":"unknown","kind":"audit"`, 1),
		strings.TrimSpace(good[:len(good)-2]) + `,"unknown_wrapper":true}` + "\n",
		strings.Replace(good, `"payload":`, `"payload":{},"payload":`, 1),
		strings.Replace(good, `"broker_time":`, `"broker_time":"invalid","broker_time":`, 1),
		strings.Replace(good, `"source_id":"execution-factory"`, `"source_id":"unknown","source_id":"execution-factory"`, 1),
	} {
		var output bytes.Buffer
		called := false
		err := publishAudit(strings.NewReader(data), &output, planDigest([]byte(data)), func() (auditPublisher, error) { called = true; return &fakePublisher{}, nil })
		if err == nil || called {
			t.Fatalf("invalid wrapper opened factory: %s", data)
		}
	}
}
