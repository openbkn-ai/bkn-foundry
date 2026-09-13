// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package database

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/config"
)

func TestApplyPool(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	ApplyPool(conn, config.DBConfig{MaxOpenConns: 7})
	if got := conn.Stats().MaxOpenConnections; got != 7 {
		t.Fatalf("MaxOpenConnections = %d, want 7", got)
	}
	// 0 keeps database/sql's default: no cap.
	ApplyPool(conn, config.DBConfig{})
	if got := conn.Stats().MaxOpenConnections; got != 7 {
		t.Fatalf("an unset cap must leave the pool alone, got %d", got)
	}
}
