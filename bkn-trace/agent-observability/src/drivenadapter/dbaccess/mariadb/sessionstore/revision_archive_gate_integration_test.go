// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

//go:build integration

package sessionstore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"github.com/go-sql-driver/mysql"
	"strings"
	"sync"
	"testing"
)

type purgeGateConnector struct {
	driver.Connector
	id              string
	entered, resume chan struct{}
	once            sync.Once
}

func (c *purgeGateConnector) Connect(ctx context.Context) (driver.Conn, error) {
	v, e := c.Connector.Connect(ctx)
	if e != nil {
		return nil, e
	}
	return &purgeGateConn{Conn: v, c: c}, nil
}

type purgeGateConn struct {
	driver.Conn
	c *purgeGateConnector
}

func (c *purgeGateConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}
func (c *purgeGateConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, q, args)
}
func (c *purgeGateConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if strings.Contains(q, "SELECT interaction_id FROM bkn_trace_interactions") && len(args) > 0 && args[0].Value == c.c.id {
		c.c.once.Do(func() { close(c.c.entered) })
		select {
		case <-c.c.resume:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, q, args)
}
func revisionPurgeGateDB(t *testing.T, dsn, id string) (*sql.DB, <-chan struct{}, chan struct{}) {
	t.Helper()
	cfg, e := mysql.ParseDSN(dsn)
	if e != nil {
		t.Fatal("test DSN invalid")
	}
	connector, e := mysql.NewConnector(cfg)
	if e != nil {
		t.Fatal(e)
	}
	gate := &purgeGateConnector{Connector: connector, id: id, entered: make(chan struct{}), resume: make(chan struct{})}
	db := sql.OpenDB(gate)
	t.Cleanup(func() { db.Close() })
	return db, gate.entered, gate.resume
}
