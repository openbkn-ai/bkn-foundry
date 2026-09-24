// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/boot"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/conf"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/auditstore"
)

// @title Agent Observability API
// @version 1.0
// @description APIs for querying agent traces from OpenSearch.
// @BasePath /api/agent-observability/v1
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description Active OAuth bearer token. Lifecycle owner identity is derived by the server.
func main() {
	if len(os.Args) > 1 && os.Args[1] == "migrate-audit-monthly" {
		if len(os.Args) != 2 {
			log.Fatal("usage: agent-observability migrate-audit-monthly")
		}
		if err := runAuditMonthlyMigration(); err != nil {
			log.Fatal(err)
		}
		return
	}
	app, err := boot.NewApp()
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Printf("agent-observability listening on :8080")
	if err := run(ctx, app); err != nil {
		log.Fatal(err)
	}
}

// runAuditMonthlyMigration is an explicit operator action. It never runs as
// part of normal application startup.
func runAuditMonthlyMigration() error {
	config, err := conf.NewCoreConfig()
	if err != nil {
		return fmt.Errorf("read Core MariaDB configuration: %w", err)
	}
	if !strings.EqualFold(config.Store, "mariadb") || config.MariaDBDSN == "" {
		return errors.New("migrate-audit-monthly requires Core MariaDB and its existing DSN Secret reference")
	}
	db, err := sql.Open("mysql", config.MariaDBDSN)
	if err != nil {
		return fmt.Errorf("open Core MariaDB: %w", err)
	}
	defer func() { _ = db.Close() }()
	store, err := auditstore.New(db)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := store.MigrateMonthlyWindow(ctx, time.Now().UTC()); err != nil {
		return fmt.Errorf("migrate current and next two UTC Audit months: %w", err)
	}
	log.Printf("Audit monthly schema migration complete: window=current+2 UTC months template_sha256=%s", auditstore.MonthlyTemplateSHA256)
	return nil
}

type application interface {
	Start() error
	Shutdown(context.Context) error
}

func run(ctx context.Context, app application) error {
	startResult := make(chan error, 1)
	go func() {
		startResult <- app.Start()
	}()

	select {
	case err := <-startResult:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	shutdownErr := app.Shutdown(shutdownCtx)
	startErr := <-startResult
	return errors.Join(shutdownErr, startErr)
}
