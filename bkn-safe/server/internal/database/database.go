// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package database opens the GORM connection via the proton-rds driver.
// proton-rds fakes Dameng(DM8)/Kingbase(KDB9) as MySQL wire at the
// database/sql level, so xinchuang is transparent here — GORM always uses the
// mysql dialect over the "proton-rds" driver. Same pattern as oss-gateway.
package database

import (
	"database/sql"
	"fmt"
	"log/slog"

	_ "github.com/kweaver-ai/proton-rds-sdk-go/driver" // registers the "proton-rds" database/sql driver

	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"bkn-safe/config"
	"bkn-safe/internal/model"
)

// Open connects to the configured database through the proton-rds driver and
// returns a *gorm.DB.
func Open(cfg config.DBConfig) (*gorm.DB, error) {
	conn, err := sql.Open("proton-rds", cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("open proton-rds: %w", err)
	}
	// All supported backends (MySQL/DM8/KDB9) speak MySQL wire via proton-rds,
	// so the mysql dialect applies uniformly.
	slog.Info("opening database", "type", cfg.Type, "host", cfg.Host, "name", cfg.Name)
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: conn}), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("gorm open: %w", err)
	}
	return db, nil
}

// Migrate creates/updates the bkn-safe schema. Casbin's own table is migrated
// by the gorm-adapter separately.
func Migrate(db *gorm.DB) error {
	if err := db.AutoMigrate(model.AllModels()...); err != nil {
		return fmt.Errorf("auto-migrate: %w", err)
	}
	return nil
}
