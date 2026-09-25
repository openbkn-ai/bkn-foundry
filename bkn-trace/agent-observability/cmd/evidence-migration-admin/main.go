// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	_ "github.com/go-sql-driver/mysql"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/evidencemigration"
)

type adminStore interface {
	ActivateArtifact(context.Context, evidencemigration.ManifestArtifact, string) error
	ReconcileManifest(context.Context, string) error
	CloseManifest(context.Context, string, string) (string, error)
}

type activeReceipt struct {
	ManifestID       string `json:"manifest_id"`
	ContractSHA      string `json:"contract_sha"`
	State            string `json:"state"`
	SourceSnapshotAt string `json:"source_snapshot_at"`
	EntryCount       string `json:"entry_count"`
	EntriesDigest    string `json:"entries_digest"`
}

type closeReceipt struct {
	ManifestID    string `json:"manifest_id"`
	State         string `json:"state"`
	ClosureDigest string `json:"closure_digest"`
}

func main() {
	dsn := os.Getenv("BKN_TRACE_CORE_MARIADB_DSN")
	if dsn == "" {
		fail(errors.New("BKN_TRACE_CORE_MARIADB_DSN is required"))
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		fail(fmt.Errorf("open Evidence migration store: %w", err))
	}
	defer db.Close()
	if err := db.PingContext(context.Background()); err != nil {
		fail(fmt.Errorf("connect Evidence migration store: %w", err))
	}
	store, err := evidencemigration.New(db)
	if err != nil {
		fail(err)
	}
	if err := run(context.Background(), os.Args[1:], store, os.Stdout); err != nil {
		fail(err)
	}
}

func run(ctx context.Context, args []string, store adminStore, output io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: evidence-migration-admin <activate|reconcile-close> [options]")
	}
	switch args[0] {
	case "activate":
		flags := flag.NewFlagSet("activate", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		manifestPath := flags.String("manifest", "", "path to the frozen payload-free manifest JSON")
		actor := flags.String("actor", "", "operator identity recorded in the manifest audit")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *manifestPath == "" || *actor == "" || flags.NArg() != 0 {
			return errors.New("activate requires --manifest and --actor")
		}
		file, err := os.Open(*manifestPath)
		if err != nil {
			return fmt.Errorf("open Evidence migration manifest: %w", err)
		}
		defer file.Close()
		decoder := json.NewDecoder(file)
		decoder.DisallowUnknownFields()
		var artifact evidencemigration.ManifestArtifact
		if err := decoder.Decode(&artifact); err != nil {
			return fmt.Errorf("decode Evidence migration manifest: %w", err)
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			return errors.New("Evidence migration manifest must contain exactly one JSON value")
		}
		if err := store.ActivateArtifact(ctx, artifact, *actor); err != nil {
			return fmt.Errorf("activate Evidence migration manifest: %w", err)
		}
		return json.NewEncoder(output).Encode(activeReceipt{
			ManifestID: artifact.ManifestID, ContractSHA: artifact.ContractSHA, State: "active",
			SourceSnapshotAt: artifact.SourceSnapshotAt, EntryCount: artifact.EntryCount, EntriesDigest: artifact.EntriesDigest,
		})
	case "reconcile-close":
		flags := flag.NewFlagSet("reconcile-close", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		manifestID := flags.String("manifest-id", "", "active Evidence migration manifest ID")
		actor := flags.String("actor", "", "operator identity recorded in the manifest audit")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *manifestID == "" || *actor == "" || flags.NArg() != 0 {
			return errors.New("reconcile-close requires --manifest-id and --actor")
		}
		if err := store.ReconcileManifest(ctx, *manifestID); err != nil {
			return fmt.Errorf("reconcile Evidence migration manifest: %w", err)
		}
		digest, err := store.CloseManifest(ctx, *manifestID, *actor)
		if err != nil {
			return fmt.Errorf("close Evidence migration manifest: %w", err)
		}
		return json.NewEncoder(output).Encode(closeReceipt{ManifestID: *manifestID, State: "closed", ClosureDigest: digest})
	default:
		return fmt.Errorf("unknown Evidence migration admin command %q", strings.TrimSpace(args[0]))
	}
}

func fail(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
