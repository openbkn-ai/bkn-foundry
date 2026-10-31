// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package projectiondlq

import (
	"bytes"
	"context"
	"errors"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionoutbox"
	"strings"
	"testing"
	"time"
)

type commandDLQStore struct {
	entry         iprojectionoutbox.DLQEntry
	reads, writes int
	request       iprojectionoutbox.ReplayRequest
	writeErr      error
}

func (s *commandDLQStore) InspectProjectionDLQ(context.Context, uint64) (iprojectionoutbox.DLQEntry, error) {
	s.reads++
	return s.entry, nil
}
func (s *commandDLQStore) ReplayProjectionDLQ(_ context.Context, r iprojectionoutbox.ReplayRequest) error {
	s.writes++
	s.request = r
	return s.writeErr
}
func TestProjectionDLQRequiresExplicitSingleIDAndAuditForWrites(t *testing.T) {
	for _, args := range [][]string{
		{}, {"--all"}, {"--id", "0"}, {"--id", "-1"}, {"--id", "1", "--id", "2"}, {"--id", "1", "extra"},
		{"--id", "1", "--apply"}, {"--id", "1", "--apply", "--operator", "user", "--reason", "why"},
		{"--id", "1", "--apply", "--operator", strings.Repeat("a", 129), "--reason", strings.Repeat("a", 129), "--repair-version", "v1"},
		{"--id", "1", "--apply", "--operator", "user", "--reason", strings.Repeat("r", 513), "--repair-version", "v1"},
	} {
		if _, err := parseProjectionDLQOptions(args); !errors.Is(err, errProjectionDLQArguments) {
			t.Fatalf("invalid arguments accepted: %v", args)
		}
	}
	var out bytes.Buffer
	if err := Run([]string{"--help"}, &out); err != nil || !strings.Contains(out.String(), "--apply") {
		t.Fatal("help should not initialize the service or require database configuration")
	}
	if got := ErrorMessage(errors.New("private DSN and password")); strings.Contains(got, "private") {
		t.Fatal("command leaks infrastructure cause")
	}
}
func TestProjectionDLQPreviewNeverWritesAndApplyReusesAuditedRequest(t *testing.T) {
	entry := iprojectionoutbox.DLQEntry{ID: 7, SourceKind: "projection", EventID: "event", OutboxID: 3, OutboxStatus: "dead", AggregateVersion: 1, EventType: "receipt.completed"}
	for _, apply := range []bool{false, true} {
		args := []string{"--id", "7"}
		if apply {
			args = append(args, "--apply", "--operator", "operator", "--reason", "mapping repaired", "--repair-version", "v1")
		}
		opts, err := parseProjectionDLQOptions(args)
		if err != nil {
			t.Fatal(err)
		}
		store := &commandDLQStore{entry: entry}
		var out bytes.Buffer
		if err := executeProjectionDLQ(context.Background(), store, opts, &out); err != nil {
			t.Fatal(err)
		}
		if store.reads != 1 || store.writes != map[bool]int{false: 0, true: 1}[apply] {
			t.Fatal("unexpected read/write count")
		}
		if apply && (store.request != opts.request || !strings.Contains(out.String(), "replay_queued")) {
			t.Fatal("audited request was not preserved")
		}
	}
}
func TestProjectionDLQRejectsNonReplayableEntriesAndConcurrentStateChanges(t *testing.T) {
	base := iprojectionoutbox.DLQEntry{ID: 7, SourceKind: "projection", OutboxID: 3, OutboxStatus: "dead", AggregateVersion: 1, EventType: "receipt.completed"}
	opts, _ := parseProjectionDLQOptions([]string{"--id", "7", "--apply", "--operator", "op", "--reason", "why", "--repair-version", "v1"})
	for _, kind := range []string{"other source", "missing", "ambiguous", "already replayed", "pending", "answer", "revision", "provenance", "no version"} {
		t.Run(kind, func(t *testing.T) {
			entry := base
			switch kind {
			case "other source":
				entry.SourceKind = "other"
			case "missing":
				entry.OutboxID = 0
			case "ambiguous":
				entry.Ambiguous = true
			case "already replayed":
				now := time.Now()
				entry.ReplayedAt = &now
			case "pending":
				entry.OutboxStatus = "pending"
			case "answer":
				entry.EventType = "artifact.persist_requested"
			case "revision":
				entry.EventType = "revision.input.sealed"
			case "provenance":
				entry.EventType = "historical_provenance.build_requested"
			case "no version":
				entry.AggregateVersion = 0
			}
			store := &commandDLQStore{entry: entry}
			var out bytes.Buffer
			if err := executeProjectionDLQ(context.Background(), store, opts, &out); !errors.Is(err, errProjectionDLQIneligible) || store.writes != 0 || !strings.Contains(out.String(), `"eligible":false`) {
				t.Fatalf("unsafe apply: err=%v writes=%d output=%s", err, store.writes, out.String())
			}
		})
	}
	failure := errors.New("state changed")
	store := &commandDLQStore{entry: base, writeErr: failure}
	var out bytes.Buffer
	if err := executeProjectionDLQ(context.Background(), store, opts, &out); !errors.Is(err, failure) || strings.Contains(out.String(), "replay_queued") {
		t.Fatal("uncertain/failed transaction reported success")
	}
}
