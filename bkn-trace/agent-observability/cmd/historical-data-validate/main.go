// historical-data-validate checks offline candidates or explicitly publishes an approved Audit-only plan.
// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/ledgersvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/kafkaaccess/auditconsumer"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/kafkaaccess/auditvalidator"
)

type input struct {
	Kind       string          `json:"kind"`
	Payload    json.RawMessage `json:"payload"`
	BrokerTime string          `json:"broker_time,omitempty"`
}

type result struct {
	Accepted         bool                `json:"accepted"`
	Reason           string              `json:"reason"`
	EventID          string              `json:"event_id,omitempty"`
	ContentHash      string              `json:"content_hash,omitempty"`
	CanonicalPayload json.RawMessage     `json:"canonical_payload,omitempty"`
	Kafka            *publicationReceipt `json:"kafka,omitempty"`
}

func main() {
	if err := runCommand(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "historical validation stream failed")
		os.Exit(1)
	}
}

func runCommand(args []string, reader io.Reader, writer io.Writer) error {
	flags := flag.NewFlagSet("historical-data-validate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	mode := flags.Bool("publish-audit", false, "Publish an approved Audit-only NDJSON plan; Kafka ACK is not database proof")
	expected := flags.String("expected-plan-sha256", "", "SHA-256 of exact approved stdin bytes, mandatory for publishing")
	qualification := flags.Bool("qualification", false, "Qualification only, authenticated loopback Kafka; release is unavailable")
	inPlace := flags.Bool("in-place-upgrade", false, "One-time 015 to 020 upgrade using this service's configured Audit broker")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	if *mode {
		if *qualification == *inPlace {
			return fmt.Errorf("release publishing unavailable; qualification required")
		}
		if *inPlace {
			return publishAudit(reader, writer, *expected, newInPlaceKafkaPublisher)
		}
		return publishAudit(reader, writer, *expected, newKafkaPublisher)
	}
	if *expected != "" || *qualification || *inPlace {
		return fmt.Errorf("publishing flag required")
	}
	return run(reader, writer)
}

func run(reader io.Reader, writer io.Writer) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	encoder := json.NewEncoder(writer)
	for scanner.Scan() {
		out := result{Reason: "input_json_invalid"}
		if candidate, err := decodeInput(scanner.Bytes()); err == nil {
			out = validate(candidate)
		}
		if err := encoder.Encode(out); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func validate(candidate input) result {
	if strictJSON(candidate.Payload) != nil {
		return result{Reason: "payload_json_invalid"}
	}
	switch candidate.Kind {
	case "audit":
		return validateAudit(candidate)
	case "evidence":
		var event ledgervo.Event
		if json.Unmarshal(candidate.Payload, &event) != nil {
			return result{Reason: "evidence_json_invalid"}
		}
		if ledgersvc.ValidateHistoricalEvent(event) != nil {
			return result{Reason: "evidence_native_invalid"}
		}
		return result{Accepted: true, Reason: "native_format_valid_not_durable_admission", EventID: event.EventID, ContentHash: event.PayloadHash, CanonicalPayload: candidate.Payload}
	case "span":
		return validateSpan(candidate.Payload)
	default:
		return result{Reason: "kind_unsupported"}
	}
}

func validateAudit(candidate input) result {
	clock, err := time.Parse(time.RFC3339Nano, candidate.BrokerTime)
	if err != nil || clock.IsZero() {
		return result{Reason: "broker_time_required"}
	}
	var identity struct {
		SourceID string `json:"source_id"`
		Target   struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		} `json:"target"`
	}
	if json.Unmarshal(candidate.Payload, &identity) != nil {
		return result{Reason: "audit_json_invalid"}
	}
	validator, err := auditvalidator.New()
	if err != nil {
		return result{Reason: "native_validator_unavailable"}
	}
	event, err := validator.Validate(context.Background(), auditconsumer.Record{
		Topic: auditconsumer.Topic, Key: []byte(identity.SourceID + "\x1f" + identity.Target.Type + "\x1f" + identity.Target.ID), Value: candidate.Payload,
		Headers: []auditconsumer.Header{{Key: auditvalidator.SchemaHeader, Value: []byte(auditvalidator.SchemaVersion)}}, BrokerTime: clock,
	})
	if err != nil {
		if auditvalidator.IsPermanent(err) {
			return result{Reason: err.Error()}
		}
		return result{Reason: "audit_validation_failed"}
	}
	return result{Accepted: true, Reason: "native_format_valid_not_published", EventID: event.EventID, ContentHash: event.ContentHash, CanonicalPayload: event.Payload}
}

func validateSpan(payload json.RawMessage) result {
	var value map[string]json.RawMessage
	if json.Unmarshal(payload, &value) != nil || value == nil {
		return result{Reason: "span_json_invalid"}
	}
	if _, ok := value["resourceSpans"]; ok {
		return result{Reason: "nested_otlp_requires_official_exporter"}
	}
	for _, field := range []struct {
		name     string
		size     int
		optional bool
	}{{"traceId", 32, false}, {"spanId", 16, false}, {"parentSpanId", 16, true}} {
		var id string
		raw, exists := value[field.name]
		if !exists && field.optional {
			continue
		}
		if json.Unmarshal(raw, &id) != nil {
			return result{Reason: "span_identity_invalid"}
		}
		if field.optional && id == "" {
			continue
		}
		decoded, err := hex.DecodeString(id)
		if len(id) != field.size || err != nil || id != strings.ToLower(id) || bytes.Equal(decoded, make([]byte, field.size/2)) {
			return result{Reason: "span_identity_invalid"}
		}
	}
	var start, end time.Time
	for _, field := range []struct {
		name        string
		destination *time.Time
	}{{"startTime", &start}, {"endTime", &end}} {
		var text string
		if json.Unmarshal(value[field.name], &text) != nil {
			return result{Reason: "span_time_invalid"}
		}
		parsed, err := time.Parse(time.RFC3339Nano, text)
		if err != nil || parsed.IsZero() {
			return result{Reason: "span_time_invalid"}
		}
		*field.destination = parsed
	}
	if end.Before(start) {
		return result{Reason: "span_time_order_invalid"}
	}
	sum := sha256.Sum256(payload)
	return result{Accepted: true, Reason: "ss4o_identity_time_valid_mapping_not_checked", ContentHash: "sha256:" + hex.EncodeToString(sum[:]), CanonicalPayload: payload}
}
