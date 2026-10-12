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
	"errors"
	"io"
	"time"
)

type publicationReceipt struct {
	Topic     string `json:"topic"`
	Partition int    `json:"partition"`
	Offset    int64  `json:"offset"`
}

type auditPublisher interface {
	Publish(context.Context, []byte) (publicationReceipt, error)
	Close() error
}

// Preflight the complete exact-byte approved plan before opening a publisher.
func publishAudit(reader io.Reader, writer io.Writer, expected string, factory func() (auditPublisher, error)) error {
	const maxPlanBytes = 256 * 1024 * 1024
	data, err := io.ReadAll(io.LimitReader(reader, maxPlanBytes+1))
	if err != nil || len(data) > maxPlanBytes {
		return errors.New("plan unreadable or too large")
	}
	digest := sha256.Sum256(data)
	if len(expected) != 64 || expected != hex.EncodeToString(digest[:]) {
		return errors.New("approved plan SHA-256 mismatch")
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	var outcomes []result
	invalid := false
	for scanner.Scan() {
		out := result{Reason: "input_json_invalid"}
		if candidate, err := decodeInput(scanner.Bytes()); err == nil {
			if candidate.Kind != "audit" {
				out.Reason = "publish_audit_only"
			} else {
				candidate.BrokerTime = time.Now().UTC().Format(time.RFC3339Nano)
				out = validate(candidate)
			}
		}
		if !out.Accepted {
			invalid = true
		}
		outcomes = append(outcomes, out)
	}
	if scanner.Err() != nil {
		return errors.New("plan record unreadable or too large")
	}
	if len(outcomes) == 0 {
		return errors.New("empty approved plan")
	}
	encoder := json.NewEncoder(writer)
	if invalid {
		for _, out := range outcomes {
			if out.Accepted {
				out.Accepted = false
				out.Reason = "not_published_invalid_plan"
			}
			out.CanonicalPayload = nil
			if err := encoder.Encode(out); err != nil {
				return err
			}
		}
		return errors.New("approved plan contains invalid candidates")
	}
	publisher, err := factory()
	if err != nil {
		for _, out := range outcomes {
			out.Accepted = false
			out.Reason = "publisher_initialization_failed"
			out.CanonicalPayload = nil
			if err := encoder.Encode(out); err != nil {
				return err
			}
		}
		return errors.New("publisher initialization failed")
	}
	defer func() { _ = publisher.Close() }()
	failed := false
	for _, out := range outcomes {
		if failed {
			out.Accepted = false
			out.Reason = "not_published_after_failure"
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			receipt, err := publisher.Publish(ctx, out.CanonicalPayload)
			cancel()
			if err != nil {
				out.Accepted = false
				out.Reason = "publish_failed_outcome_may_be_unknown"
				failed = true
			} else {
				out.Kafka = &receipt
				out.Reason = "kafka_ack_not_database_confirmation"
			}
		}
		out.CanonicalPayload = nil
		if err := encoder.Encode(out); err != nil {
			return err
		}
	}
	if failed {
		return errors.New("one or more candidates were not acknowledged")
	}
	return nil
}
