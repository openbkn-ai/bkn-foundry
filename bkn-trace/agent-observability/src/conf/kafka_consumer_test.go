// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package conf

import "testing"

func TestKafkaConsumerConfigDisabledByDefault(t *testing.T) {
	t.Setenv("BKN_TRACE_EVIDENCE_KAFKA_ENABLED", "")
	t.Setenv("BKN_TRACE_AUDIT_KAFKA_ENABLED", "")
	config, err := NewKafkaConsumerConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.Evidence.Enabled || config.Audit.Enabled {
		t.Fatalf("Kafka consumers must remain disabled by default: %+v", config)
	}
}

func TestKafkaConsumerConfigRequiresCompleteEnabledConsumerConfig(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func()
	}{
		{name: "missing brokers", setup: func() { t.Setenv("BKN_TRACE_EVIDENCE_KAFKA_ENABLED", "true") }},
		{name: "invalid evidence group", setup: func() {
			t.Setenv("BKN_TRACE_EVIDENCE_KAFKA_ENABLED", "true")
			setEvidenceKafka(t, "kafka:9092", "PLAIN", "user", "password")
			t.Setenv("BKN_TRACE_EVIDENCE_KAFKA_GROUP", "custom-group")
		}},
		{name: "missing audit group", setup: func() {
			t.Setenv("BKN_TRACE_AUDIT_KAFKA_ENABLED", "true")
			setAuditKafka(t, "kafka:9092", "PLAIN", "user", "password")
		}},
		{name: "unknown SASL mechanism", setup: func() {
			t.Setenv("BKN_TRACE_EVIDENCE_KAFKA_ENABLED", "true")
			setEvidenceKafka(t, "kafka:9092", "GSSAPI", "user", "password")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, key := range []string{
				"BKN_TRACE_EVIDENCE_KAFKA_ENABLED", "BKN_TRACE_AUDIT_KAFKA_ENABLED",
				"BKN_TRACE_EVIDENCE_KAFKA_BROKERS", "BKN_TRACE_EVIDENCE_KAFKA_GROUP", "BKN_TRACE_EVIDENCE_KAFKA_SASL_MECHANISM", "BKN_TRACE_EVIDENCE_KAFKA_USERNAME", "BKN_TRACE_EVIDENCE_KAFKA_PASSWORD",
				"BKN_TRACE_AUDIT_KAFKA_BROKERS", "BKN_TRACE_AUDIT_KAFKA_GROUP", "BKN_TRACE_AUDIT_KAFKA_SASL_MECHANISM", "BKN_TRACE_AUDIT_KAFKA_USERNAME", "BKN_TRACE_AUDIT_KAFKA_PASSWORD",
			} {
				t.Setenv(key, "")
			}
			test.setup()
			if _, err := NewKafkaConsumerConfig(); err == nil {
				t.Fatal("expected invalid enabled configuration to fail")
			}
		})
	}
}

func TestKafkaConsumerConfigAcceptsIndependentExistingSecretCredentials(t *testing.T) {
	t.Setenv("BKN_TRACE_EVIDENCE_KAFKA_ENABLED", "true")
	t.Setenv("BKN_TRACE_AUDIT_KAFKA_ENABLED", "true")
	setEvidenceKafka(t, "evidence-0:9092,evidence-1:9092", "SCRAM-SHA-512", "evidence-user", "evidence-password")
	setAuditKafka(t, "audit-0:9092,audit-1:9092", "SCRAM-SHA-256", "audit-user", "audit-password")
	t.Setenv("BKN_TRACE_EVIDENCE_KAFKA_GROUP", EvidenceKafkaConsumerGroup)
	t.Setenv("BKN_TRACE_AUDIT_KAFKA_GROUP", "bkn-trace-audit-ledger-v1")
	config, err := NewKafkaConsumerConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !config.Evidence.Enabled || !config.Audit.Enabled || config.Evidence.Group != EvidenceKafkaConsumerGroup || config.Audit.Group != "bkn-trace-audit-ledger-v1" || config.Evidence.Brokers[0] == config.Audit.Brokers[0] {
		t.Fatalf("unexpected independent consumer config: %+v", config)
	}
}

func setEvidenceKafka(t *testing.T, brokers, mechanism, username, password string) {
	t.Helper()
	t.Setenv("BKN_TRACE_EVIDENCE_KAFKA_BROKERS", brokers)
	t.Setenv("BKN_TRACE_EVIDENCE_KAFKA_SASL_MECHANISM", mechanism)
	t.Setenv("BKN_TRACE_EVIDENCE_KAFKA_USERNAME", username)
	t.Setenv("BKN_TRACE_EVIDENCE_KAFKA_PASSWORD", password)
}

func setAuditKafka(t *testing.T, brokers, mechanism, username, password string) {
	t.Helper()
	t.Setenv("BKN_TRACE_AUDIT_KAFKA_BROKERS", brokers)
	t.Setenv("BKN_TRACE_AUDIT_KAFKA_SASL_MECHANISM", mechanism)
	t.Setenv("BKN_TRACE_AUDIT_KAFKA_USERNAME", username)
	t.Setenv("BKN_TRACE_AUDIT_KAFKA_PASSWORD", password)
}
