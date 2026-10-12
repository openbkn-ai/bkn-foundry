// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"net"
	"os"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/auditstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/kafkaaccess/auditconsumer"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/kafkaaccess/auditvalidator"
)

type integrationCommitter struct{ count int }

func (c *integrationCommitter) Commit(context.Context, int, int64) error { c.count++; return nil }

// This test writes only to an explicitly selected loopback isolated database.
// Kafka transport and offset commits are synthetic; validator, consumer,
// transactional store and reader are the production implementations.
func TestHistoricalAuditPipelineIntegration(t *testing.T) {
	dsn, file := os.Getenv("BKN_HISTORY_INTEGRATION_DSN"), os.Getenv("BKN_HISTORY_INTEGRATION_AUDIT_FILE")
	if dsn == "" || file == "" {
		t.Skip("explicit isolated DSN and private native Audit fixture required")
	}
	config, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid isolated database configuration")
	}
	host, _, err := net.SplitHostPort(config.Addr)
	if err != nil || config.Net != "tcp" || (host != "127.0.0.1" && host != "localhost" && host != "::1") {
		t.Fatal("integration target must be an isolated loopback TCP database")
	}
	if config.DBName != "bkn_trace" && config.DBName != "bkn_audit" {
		t.Fatal("integration target must use the restored native database namespace")
	}
	config.ParseTime = true
	config.Loc = time.UTC
	db, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		t.Fatal("open isolated database failed")
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if db.PingContext(ctx) != nil {
		t.Fatal("isolated database unavailable")
	}
	if _, err := db.ExecContext(ctx, "SET SESSION time_zone='+00:00'"); err != nil {
		t.Fatal("set isolated UTC session failed")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal("private native fixture unavailable")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	var first json.RawMessage
	if decoder.Decode(&first) != nil {
		t.Fatal("native fixture JSON invalid")
	}
	var request input
	if json.Unmarshal(first, &request) != nil {
		t.Fatal("native fixture JSON invalid")
	}
	payload := first
	if request.Kind != "" {
		if request.Kind != "audit" || len(request.Payload) == 0 {
			t.Fatal("first fixture must be an Audit candidate")
		}
		payload = request.Payload
	}
	var identity struct {
		SourceID   string                    `json:"source_id"`
		OccurredAt string                    `json:"occurred_at"`
		Target     struct{ Type, ID string } `json:"target"`
	}
	if json.Unmarshal(payload, &identity) != nil {
		t.Fatal("native Audit fixture invalid")
	}
	clockText := request.BrokerTime
	if clockText == "" {
		clockText = identity.OccurredAt
	}
	clock, err := time.Parse(time.RFC3339Nano, clockText)
	if err != nil {
		t.Fatal("synthetic validation transport timestamp invalid")
	}
	sum := sha256.Sum256(payload)
	record := auditconsumer.Record{Topic: auditconsumer.Topic, Key: []byte(identity.SourceID + "\x1f" + identity.Target.Type + "\x1f" + identity.Target.ID), Value: payload, Headers: []auditconsumer.Header{{Key: auditvalidator.SchemaHeader, Value: []byte(auditvalidator.SchemaVersion)}}, BrokerTime: clock, Partition: 2147483647, Offset: int64(binary.BigEndian.Uint64(sum[:8]) & 0x3fffffffffffffff)}
	validator, err := auditvalidator.New()
	if err != nil {
		t.Fatal("native validator initialization failed")
	}
	event, err := validator.Validate(ctx, record)
	if err != nil {
		t.Fatal("native fixture validation rejected")
	}
	store, err := auditstore.New(db)
	if err != nil {
		t.Fatal("native store initialization failed")
	}
	committer := &integrationCommitter{}
	consumer, err := auditconsumer.New(validator, store, committer)
	if err != nil {
		t.Fatal("native consumer initialization failed")
	}
	for attempt := 0; attempt < 2; attempt++ {
		if consumer.Process(ctx, record) != nil {
			t.Fatal("native consumer/store processing failed")
		}
	}
	if committer.count != 2 {
		t.Fatal("synthetic offset discipline did not complete twice")
	}
	reader, err := auditstore.NewReader(db)
	if err != nil {
		t.Fatal("native reader initialization failed")
	}
	persisted, found, err := reader.Get(ctx, event.EventID)
	if err != nil || !found || persisted.EventID != event.EventID || !persisted.OccurredAt.Equal(event.OccurredAt.Truncate(time.Microsecond)) {
		t.Fatal("native reader failed identity/original time readback")
	}
	var count int
	var hash string
	if db.QueryRowContext(ctx, "SELECT COUNT(*),MIN(content_hash) FROM bkn_audit.audit_event_dedup WHERE event_id=?", event.EventID).Scan(&count, &hash) != nil || count != 1 || hash != event.ContentHash {
		t.Fatal("dedup readback failed")
	}
	table := "bkn_audit.audit_event_" + event.OccurredAt.UTC().Format("200601")
	if db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE event_id=?", event.EventID).Scan(&count) != nil || count != 1 {
		t.Fatal("repeat append created multiple native rows")
	}
	var persistedPayload []byte
	if db.QueryRowContext(ctx, "SELECT payload FROM "+table+" WHERE event_id=?", event.EventID).Scan(&persistedPayload) != nil || !bytes.Equal(persistedPayload, event.Payload) {
		t.Fatal("native canonical payload changed in storage")
	}
	t.Log("production validator -> consumer -> MariaDB store -> reader passed; two synthetic deliveries, one row; not Kafka/UI end-to-end")
}
