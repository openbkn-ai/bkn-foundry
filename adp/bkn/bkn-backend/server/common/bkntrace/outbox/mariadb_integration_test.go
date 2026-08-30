// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package outbox

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/openbkn-ai/bkn-foundry/comm-go/db/driver"
)

func TestMariaDBRepositoryIntegration(t *testing.T) {
	dsn := os.Getenv("BKN_TRACE_OUTBOX_INTEGRATION_DSN")
	if dsn == "" {
		t.Skip("BKN_TRACE_OUTBOX_INTEGRATION_DSN is not configured")
	}
	db, err := sql.Open("openbkn-rds", dsn)
	if err != nil {
		t.Fatalf("open MariaDB: %v", err)
	}
	defer db.Close()
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("ping MariaDB: %v", err)
	}

	suffix := fmt.Sprintf("e2e-%d", time.Now().UnixNano())
	streamID := "bkn-backend-" + suffix
	defer func() {
		_, _ = db.Exec("DELETE FROM "+tableOutbox+" WHERE producer_stream_id = ?", streamID)
		_, _ = db.Exec("DELETE FROM "+tableStream+" WHERE producer_id = ? AND producer_stream_id = ?", "integration-test", streamID)
	}()

	repository, err := NewRepository(db, Config{
		ProducerID: "integration-test", ProducerStreamID: streamID, DatabaseType: "MARIADB",
		IngestURL: "http://core.invalid", QueryGatewayToken: "integration-token",
	})
	if err != nil {
		t.Fatalf("new repository: %v", err)
	}
	now := time.Now().UTC()
	owner := Owner{
		TenantID: "e2e", ApplicationPrincipalID: "integration-test",
		EffectiveSubjectType: "service", EffectiveSubjectID: "integration-test",
	}
	first, err := repository.Enqueue(context.Background(), integrationEvent(suffix+"-1", now), owner)
	if err != nil {
		t.Fatalf("enqueue first event: %v", err)
	}
	second, err := repository.Enqueue(context.Background(), integrationEvent(suffix+"-2", now), owner)
	if err != nil {
		t.Fatalf("enqueue second event: %v", err)
	}
	if first.ProducerEpoch != initialProducerEpoch || second.ProducerEpoch != initialProducerEpoch ||
		first.ProducerSequence != 1 || second.ProducerSequence != 2 {
		t.Fatalf("unexpected producer ordering: first=%d/%d second=%d/%d", first.ProducerEpoch, first.ProducerSequence, second.ProducerEpoch, second.ProducerSequence)
	}
	workerRepository, err := NewRepository(db, Config{
		ProducerID: "integration-test", ProducerStreamID: streamID, DatabaseType: "MARIADB",
		IngestURL: "http://core.invalid", QueryGatewayToken: "integration-token", BumpEpochOnStart: true,
	})
	if err != nil {
		t.Fatalf("start worker repository: %v", err)
	}
	if workerRepository.epoch != initialProducerEpoch+1 {
		t.Fatalf("worker epoch = %d, want %d", workerRepository.epoch, initialProducerEpoch+1)
	}
	third, err := repository.Enqueue(context.Background(), integrationEvent(suffix+"-3", now), owner)
	if err != nil {
		t.Fatalf("enqueue post-worker event: %v", err)
	}
	if third.ProducerEpoch != initialProducerEpoch+1 || third.ProducerSequence != 1 {
		t.Fatalf("post-worker event ordering = %d/%d, want 2/1", third.ProducerEpoch, third.ProducerSequence)
	}

	claimAt := time.Now().UTC()
	record, err := repository.ClaimHeadOfLine(context.Background(), claimAt)
	if err != nil || record == nil || record.Event.EventID != first.EventID {
		t.Fatalf("claim first event = %#v, %v", record, err)
	}
	if _, err := repository.Complete(context.Background(), record, StatusRetry, "core_timeout", claimAt.Add(time.Minute)); err != nil {
		t.Fatalf("schedule retry: %v", err)
	}
	blocked, err := repository.ClaimHeadOfLine(context.Background(), claimAt)
	if err != nil || blocked != nil {
		t.Fatalf("later sequence must remain blocked during retry, got %#v, %v", blocked, err)
	}
	if _, err := db.Exec("UPDATE "+tableOutbox+" SET available_at = ? WHERE event_id = ?", claimAt, first.EventID); err != nil {
		t.Fatalf("make retry available: %v", err)
	}
	retry, err := repository.ClaimHeadOfLine(context.Background(), time.Now().UTC())
	if err != nil || retry == nil || retry.Event.EventID != first.EventID {
		t.Fatalf("reclaim first event = %#v, %v", retry, err)
	}
	if updated, err := repository.Complete(context.Background(), retry, StatusDelivered, "", time.Time{}); err != nil || !updated {
		t.Fatalf("deliver first event = %t, %v", updated, err)
	}
	next, err := repository.ClaimHeadOfLine(context.Background(), time.Now().UTC())
	if err != nil || next == nil || next.Event.EventID != second.EventID {
		t.Fatalf("claim second event = %#v, %v", next, err)
	}
	if updated, err := repository.Complete(context.Background(), next, StatusDelivered, "", time.Time{}); err != nil || !updated {
		t.Fatalf("deliver second event = %t, %v", updated, err)
	}
	last, err := repository.ClaimHeadOfLine(context.Background(), time.Now().UTC())
	if err != nil || last == nil || last.Event.EventID != third.EventID {
		t.Fatalf("claim post-worker event = %#v, %v", last, err)
	}
	if updated, err := repository.Complete(context.Background(), last, StatusDelivered, "", time.Time{}); err != nil || !updated {
		t.Fatalf("deliver post-worker event = %t, %v", updated, err)
	}
}

func integrationEvent(eventID string, now time.Time) Event {
	return Event{
		EventID: eventID, EventType: "e2e", ConversationID: "e2e", InteractionID: "e2e",
		StartedAt: now, ObservedAt: now, EmittedAt: now, Envelope: []byte(`{"e2e":true}`),
	}
}
