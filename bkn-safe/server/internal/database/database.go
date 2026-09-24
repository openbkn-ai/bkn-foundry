// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package database opens the GORM connection via the openbkn-rds driver.
// openbkn-rds fakes Dameng(DM8)/Kingbase(KDB9) as MySQL wire at the
// database/sql level, so xinchuang is transparent here — GORM always uses the
// mysql dialect over the "openbkn-rds" driver. Same pattern as oss-gateway.
package database

import (
	"database/sql"
	"fmt"
	"log/slog"

	_ "github.com/openbkn-ai/bkn-foundry/comm-go/db/driver" // registers the "openbkn-rds" database/sql driver
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/config"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

// Open connects to the configured database through the openbkn-rds driver and
// returns a *gorm.DB.
func Open(cfg config.DBConfig) (*gorm.DB, error) {
	conn, err := sql.Open("openbkn-rds", cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("open openbkn-rds: %w", err)
	}
	ApplyPool(conn, cfg)
	// All supported backends (MySQL/DM8/KDB9) speak MySQL wire via openbkn-rds,
	// so the mysql dialect applies uniformly.
	slog.Info("opening database", "type", cfg.Type, "host", cfg.Host, "name", cfg.Name)
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: conn}), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("gorm open: %w", err)
	}
	return db, nil
}

// ApplyPool sizes the connection pool. Without a cap every concurrent request
// holds its own connection, so a burst of authorization writes could open
// hundreds of connections to a database the other services share; with the
// cap, callers beyond it wait for a free connection under their own context
// (#1511). Idle connections are kept up to the cap: database/sql keeps two by
// default, which makes a burst reconnect for almost every request.
func ApplyPool(conn *sql.DB, cfg config.DBConfig) {
	if cfg.MaxOpenConns > 0 {
		conn.SetMaxOpenConns(cfg.MaxOpenConns)
		conn.SetMaxIdleConns(cfg.MaxOpenConns)
	}
	if cfg.ConnMaxIdleTime > 0 {
		conn.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)
	}
	if cfg.ConnMaxLifetime > 0 {
		conn.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	}
}

// Migrate creates/updates the bkn-safe schema. Casbin's own table is migrated
// by the gorm-adapter separately.
func Migrate(db *gorm.DB) error {
	if err := db.AutoMigrate(model.AllModels()...); err != nil {
		return fmt.Errorf("auto-migrate: %w", err)
	}
	return nil
}
