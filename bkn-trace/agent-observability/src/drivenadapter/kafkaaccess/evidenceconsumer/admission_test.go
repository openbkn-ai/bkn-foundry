// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidenceconsumer

import (
	"testing"
	"time"
)

func closureTestRecord(sequence uint64, brokerTimestamp string, instance string) Record {
	return Record{
		Key:             "bkn-backend:aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		TimestampType:   "LogAppendTime",
		BrokerTimestamp: brokerTimestamp,
		Headers: []Header{
			{Key: "content-type", Value: "application/json"},
			{Key: "bkn-trace-schema-version", Value: "3.0.0"},
			{Key: "capture_policy_revision", Value: "41"},
			{Key: "producer_instance_id", Value: instance},
			{Key: "bkn-evidence-record-class", Value: "live"},
		},
		ProducerStreamID: "bkn-backend:aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		ProducerSequence: sequence,
	}
}

func closureTestPolicy() PolicySnapshot {
	return PolicySnapshot{
		Revision:           41,
		Enabled:            true,
		InstanceID:         "spiffe://cluster.local/ns/openbkn/sa/bkn-backend#aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		RegisteredRevision: 41,
		Closure: ClosureWatermark{
			InstanceID:           "spiffe://cluster.local/ns/openbkn/sa/bkn-backend#aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
			Revision:             41,
			LastAcceptedSequence: 7,
			ClosedAt:             "2026-09-22T08:00:10.000Z",
		},
	}
}

func TestAdmitLiveAcceptsEqualSequenceAndLogAppendBoundary(t *testing.T) {
	decision := AdmitLive(closureTestRecord(7, "2026-09-22T08:00:10.000Z", closureTestPolicy().InstanceID), closureTestPolicy())
	if decision != (Decision{Decision: "accept", Reason: "accepted_live"}) {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestAdmitLiveRejectsSequenceAfterClosureBeforeTimestamp(t *testing.T) {
	decision := AdmitLive(closureTestRecord(8, "2026-09-22T08:00:09.999Z", closureTestPolicy().InstanceID), closureTestPolicy())
	if decision.Reason != "producer_sequence_after_closure" {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestAdmitLiveRejectsLogAppendAfterClosure(t *testing.T) {
	decision := AdmitLive(closureTestRecord(7, "2026-09-22T08:00:10.001Z", closureTestPolicy().InstanceID), closureTestPolicy())
	if decision.Reason != "record_appended_after_closure" {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestAdmitLiveRestartedInstanceDoesNotReuseOldClosure(t *testing.T) {
	restarted := closureTestPolicy()
	restarted.Revision = 42
	restarted.RegisteredRevision = 42
	restarted.InstanceID = "spiffe://cluster.local/ns/openbkn/sa/bkn-backend#bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	restarted.Closure.InstanceID = closureTestPolicy().InstanceID
	restarted.Closure.Revision = 41
	record := closureTestRecord(1, "2026-09-22T08:01:00.000Z", restarted.InstanceID)
	record.Key = "bkn-backend:bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	record.ProducerStreamID = record.Key
	record.Headers[2].Value = "42"
	decision := AdmitLive(record, restarted)
	if decision != (Decision{Decision: "accept", Reason: "accepted_live"}) {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestParseHeadersRejectsDuplicateAndRequiresExactLiveSet(t *testing.T) {
	_, err := ParseHeaders([]Header{
		{Key: "content-type", Value: "application/json"},
		{Key: "content-type", Value: "application/json"},
	})
	if err == nil {
		t.Fatal("ParseHeaders() accepted duplicate header")
	}
	if _, err := time.Parse(time.RFC3339Nano, "2026-09-22T08:00:10.000Z"); err != nil {
		t.Fatal(err)
	}
	valid := closureTestRecord(7, "2026-09-22T08:00:10.000Z", closureTestPolicy().InstanceID)
	if err := ValidateLiveRecordContract(valid); err != nil {
		t.Fatalf("valid record rejected: %v", err)
	}
	valid.Headers = append(valid.Headers, Header{Key: "bkn-evidence-migration-id", Value: "forbidden"})
	if err := ValidateLiveRecordContract(valid); err == nil {
		t.Fatal("live record with migration header accepted")
	}
}
