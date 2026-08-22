// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package projectionrebuildsvc_test

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/projectionrebuildsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionoutbox"
)

func TestRebuildUsesAuthoritativeStateAfterDeliveredOutboxWasCleared(t *testing.T) {
	t.Parallel()

	source := &fakeSource{authoritative: []iprojectionoutbox.Item{
		projectionItem("conversation", "conv-1", `{"status":"active"}`),
		projectionItem("interaction", "int-1", `{"status":"completed"}`),
		projectionItem("receipt", "rcpt-1", `{"status":"completed"}`),
	}}
	target := newFakeTarget()
	service := projectionrebuildsvc.New(source, target, projectionrebuildsvc.Options{BatchSize: 2})

	result, err := service.Rebuild(
		context.Background(), "core", "bkn-trace-core", "bkn-trace-core-v20260730",
	)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if result.LastOutboxID != 0 || result.ProjectedCount != 3 ||
		target.alias != "bkn-trace-core" || target.version != result.IndexVersion {
		t.Fatalf("unexpected rebuild result: %#v target=%#v", result, target)
	}
	if len(source.checkpoints) != 1 || source.checkpoints[0] != 0 {
		t.Fatalf("rebuild start checkpoint was not recorded: %#v", source.checkpoints)
	}
}

func TestRebuildDoesNotSwitchAliasWhenDocumentContentValidationFails(t *testing.T) {
	t.Parallel()

	source := &fakeSource{authoritative: []iprojectionoutbox.Item{
		projectionItem("conversation", "conv-1", `{"status":"active"}`),
	}}
	target := newFakeTarget()
	target.corrupt = true
	service := projectionrebuildsvc.New(source, target, projectionrebuildsvc.Options{})

	_, err := service.Rebuild(context.Background(), "core", "alias", "version")
	if !errors.Is(err, projectionrebuildsvc.ErrProjectionValidation) || target.alias != "" {
		t.Fatalf("invalid rebuild switched alias: alias=%q err=%v", target.alias, err)
	}
}

func TestRebuildPreservesProjectionInfrastructureFailure(t *testing.T) {
	t.Parallel()

	infrastructureErr := errors.New("OpenSearch timeout")
	source := &fakeSource{authoritative: []iprojectionoutbox.Item{
		projectionItem("conversation", "conv-1", `{"status":"active"}`),
	}}
	target := newFakeTarget()
	target.validateErr = infrastructureErr
	service := projectionrebuildsvc.New(source, target, projectionrebuildsvc.Options{})

	_, err := service.Rebuild(context.Background(), "core", "alias", "version")
	if !errors.Is(err, infrastructureErr) {
		t.Fatalf("projection infrastructure failure was replaced: %v", err)
	}
	if errors.Is(err, projectionrebuildsvc.ErrProjectionValidation) {
		t.Fatalf("infrastructure failure was misclassified as data divergence: %v", err)
	}
}

func TestRebuildCatchesUpEventsCommittedDuringSnapshotBeforeAliasSwitch(t *testing.T) {
	t.Parallel()

	source := &fakeSource{
		authoritative: []iprojectionoutbox.Item{
			projectionItem("conversation", "conv-1", `{"status":"active"}`),
			projectionItem("interaction", "int-1", `{"status":"active"}`),
		},
		outbox: []iprojectionoutbox.Item{
			{
				ID: 3, AggregateType: "receipt", AggregateID: "rcpt-1",
				EventID: "evt-3", Payload: []byte(`{"status":"completed"}`),
			},
		},
		authoritativeCount: 3,
		highWatermarks:     []uint64{2, 3, 3},
	}
	target := newFakeTarget()
	service := projectionrebuildsvc.New(source, target, projectionrebuildsvc.Options{BatchSize: 1})

	result, err := service.Rebuild(
		context.Background(), "core", "bkn-trace-core", "bkn-trace-core-v20260730",
	)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if result.LastOutboxID != 3 || result.ProjectedCount != 3 {
		t.Fatalf("rebuild did not catch up: %#v", result)
	}
	if target.alias == "" {
		t.Fatal("validated caught-up projection did not switch alias")
	}
}

func projectionItem(aggregateType, aggregateID, payload string) iprojectionoutbox.Item {
	return iprojectionoutbox.Item{
		AggregateType: aggregateType, AggregateID: aggregateID,
		AggregateVersion: 1,
		EventType:        aggregateType + ".snapshot", EventID: aggregateType + ":" + aggregateID,
		Payload: []byte(payload),
	}
}

type fakeSource struct {
	authoritative      []iprojectionoutbox.Item
	outbox             []iprojectionoutbox.Item
	authoritativeCount uint64
	checkpoints        []uint64
	highWatermarks     []uint64
	highWatermark      int
}

func (s *fakeSource) ProjectionHighWatermark(context.Context) (uint64, error) {
	if len(s.highWatermarks) > 0 {
		index := s.highWatermark
		if index >= len(s.highWatermarks) {
			index = len(s.highWatermarks) - 1
		}
		s.highWatermark++
		return s.highWatermarks[index], nil
	}
	if len(s.outbox) == 0 {
		return 0, nil
	}
	return s.outbox[len(s.outbox)-1].ID, nil
}

func (s *fakeSource) ScanAuthoritativeProjection(
	_ context.Context,
	afterType string,
	afterID string,
	limit int,
) ([]iprojectionoutbox.Item, error) {
	items := append([]iprojectionoutbox.Item(nil), s.authoritative...)
	sort.Slice(items, func(i, j int) bool {
		if items[i].AggregateType == items[j].AggregateType {
			return items[i].AggregateID < items[j].AggregateID
		}
		return items[i].AggregateType < items[j].AggregateType
	})
	var result []iprojectionoutbox.Item
	for _, item := range items {
		if item.AggregateType < afterType ||
			item.AggregateType == afterType && item.AggregateID <= afterID {
			continue
		}
		result = append(result, item)
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

func (s *fakeSource) CountAuthoritativeProjection(context.Context) (uint64, error) {
	if s.authoritativeCount > 0 {
		return s.authoritativeCount, nil
	}
	return uint64(len(s.authoritative)), nil
}

func (s *fakeSource) ScanProjectionHistory(
	_ context.Context,
	after uint64,
	through uint64,
	limit int,
) ([]iprojectionoutbox.Item, error) {
	var result []iprojectionoutbox.Item
	for _, item := range s.outbox {
		if item.ID > after && item.ID <= through {
			result = append(result, item)
			if len(result) == limit {
				break
			}
		}
	}
	return result, nil
}

func (s *fakeSource) LoadProjectionCheckpoint(context.Context, string, string) (uint64, error) {
	if len(s.checkpoints) == 0 {
		return 0, nil
	}
	return s.checkpoints[len(s.checkpoints)-1], nil
}

func (s *fakeSource) SaveProjectionCheckpoint(_ context.Context, _, _ string, value uint64) error {
	s.checkpoints = append(s.checkpoints, value)
	return nil
}

type fakeTarget struct {
	documents   map[string][]byte
	corrupt     bool
	validateErr error
	alias       string
	version     string
}

func newFakeTarget() *fakeTarget {
	return &fakeTarget{documents: make(map[string][]byte)}
}

func (t *fakeTarget) PrepareVersion(context.Context, string) error { return nil }

func (t *fakeTarget) ProjectVersion(_ context.Context, _ string, item iprojectionoutbox.Item) error {
	payload := append([]byte(nil), item.Payload...)
	if t.corrupt {
		payload = []byte(`{"corrupt":true}`)
	}
	t.documents[iprojectionoutbox.DocumentID(item)] = payload
	return nil
}

func (t *fakeTarget) ValidateVersion(
	_ context.Context,
	_ string,
	items []iprojectionoutbox.Item,
) error {
	if t.validateErr != nil {
		return t.validateErr
	}
	for _, item := range items {
		if !bytes.Equal(t.documents[iprojectionoutbox.DocumentID(item)], item.Payload) {
			return projectionrebuildsvc.ErrProjectionValidation
		}
	}
	return nil
}

func (t *fakeTarget) CountVersion(context.Context, string) (uint64, error) {
	return uint64(len(t.documents)), nil
}

func (t *fakeTarget) SwitchAlias(_ context.Context, alias, version string) error {
	t.alias, t.version = alias, version
	return nil
}
