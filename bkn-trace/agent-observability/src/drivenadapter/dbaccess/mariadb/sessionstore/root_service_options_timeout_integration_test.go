// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

//go:build integration

package sessionstore

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

func TestRootServiceOptionsRealServerTimeout(t *testing.T) {
	dsn := os.Getenv("BKN_ROOT_SERVICE_TIMEOUT_DSN")
	if dsn == "" {
		t.Skip("isolated MariaDB required")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid test DSN")
	}
	if (cfg.Addr != "127.0.0.1:13340" || cfg.DBName != "root_service_timeout") && (cfg.Addr != "127.0.0.1:3306" || cfg.DBName != "bkn_trace_ci") {
		t.Fatal("refuse non-isolated endpoint")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	var id int64
	var before, after float64
	if err = conn.QueryRowContext(ctx, "SELECT CONNECTION_ID(),@@SESSION.max_statement_time").Scan(&id, &before); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	var slept int
	err = conn.QueryRowContext(ctx, rootServiceBudgetStatement("SELECT SLEEP(?)"), 10).Scan(&slept)
	elapsed := time.Since(start)
	var serverErr *mysql.MySQLError
	if !errors.As(err, &serverErr) || serverErr.Number != 1969 || !errors.Is(rootServiceQueryError(err), context.DeadlineExceeded) {
		t.Fatalf("not server timeout: %v", err)
	}
	if elapsed < 2*time.Second || elapsed > 8*time.Second {
		t.Fatalf("unexpected duration %s", elapsed)
	}
	if err = conn.QueryRowContext(ctx, "SELECT @@SESSION.max_statement_time").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("session configuration changed")
	}
	var active int
	if err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE ID=? AND COMMAND<>'Sleep'", id).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 0 {
		t.Fatal("timed out statement remains active")
	}
	t.Logf("server error=1969 duration=%s session setting unchanged; connection idle", elapsed)
}
