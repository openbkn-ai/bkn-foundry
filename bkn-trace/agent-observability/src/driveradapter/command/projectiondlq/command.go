// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package projectiondlq

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	_ "github.com/go-sql-driver/mysql"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/conf"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/projectorsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionoutbox"
)

const projectionDLQUsage = "usage: agent-observability projection-dlq --id <single DLQ ID> [--apply --operator <name> --reason <reason> --repair-version <version>]"

var errProjectionDLQArguments = errors.New(projectionDLQUsage)
var errProjectionDLQIneligible = errors.New("DLQ entry is not eligible; inspect the preview reason")

type projectionDLQOptions struct {
	request iprojectionoutbox.ReplayRequest
	apply   bool
}
type projectionDLQStore interface {
	InspectProjectionDLQ(context.Context, uint64) (iprojectionoutbox.DLQEntry, error)
	ReplayProjectionDLQ(context.Context, iprojectionoutbox.ReplayRequest) error
}

func parseProjectionDLQOptions(args []string) (projectionDLQOptions, error) {
	var opts projectionDLQOptions
	flags := flag.NewFlagSet("projection-dlq", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var idSeen bool
	flags.Func("id", "One explicit DLQ ID", func(value string) error {
		if idSeen {
			return errProjectionDLQArguments
		}
		idSeen = true
		id, err := strconv.ParseUint(value, 10, 64)
		opts.request.DLQID = id
		return err
	})
	flags.BoolVar(&opts.apply, "apply", false, "Queue an audited replay; default is read-only preview")
	flags.StringVar(&opts.request.Operator, "operator", "", "Operator recorded in the audit (required for apply)")
	flags.StringVar(&opts.request.Reason, "reason", "", "Reason recorded in the audit (required for apply)")
	flags.StringVar(&opts.request.RepairVersion, "repair-version", "", "Repair version recorded in the audit (required for apply)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return opts, flag.ErrHelp
		}
		return opts, errProjectionDLQArguments
	}
	if flags.NArg() != 0 || opts.request.DLQID == 0 {
		return opts, errProjectionDLQArguments
	}
	opts.request.Operator = strings.TrimSpace(opts.request.Operator)
	opts.request.Reason = strings.TrimSpace(opts.request.Reason)
	opts.request.RepairVersion = strings.TrimSpace(opts.request.RepairVersion)
	if opts.apply {
		for _, field := range []struct {
			value string
			limit int
		}{{opts.request.Operator, 128}, {opts.request.Reason, 512}, {opts.request.RepairVersion, 128}} {
			value, limit := field.value, field.limit
			if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > limit {
				return opts, errProjectionDLQArguments
			}
		}
	}
	return opts, nil
}
func projectionDLQEligibility(entry iprojectionoutbox.DLQEntry) string {
	switch {
	case entry.SourceKind != "projection":
		return "not_projection"
	case entry.Ambiguous:
		return "ambiguous_outbox"
	case entry.OutboxID == 0:
		return "outbox_missing"
	case entry.ReplayedAt != nil:
		return "already_replayed"
	case entry.OutboxStatus != "dead":
		return "outbox_not_dead"
	case !projectorsvc.IsSearchProjectionEvent(entry.EventType):
		return "special_handler_not_supported"
	case entry.AggregateVersion == 0:
		return "aggregate_version_missing"
	default:
		return "eligible"
	}
}
func executeProjectionDLQ(ctx context.Context, store projectionDLQStore, opts projectionDLQOptions, out io.Writer) error {
	entry, err := store.InspectProjectionDLQ(ctx, opts.request.DLQID)
	if err != nil {
		return err
	}
	reason := projectionDLQEligibility(entry)
	action := "preview"
	if opts.apply && reason == "eligible" {
		// Reuse the existing row-lock, dead-state revalidation and audit transaction.
		// No worker, migration, HTTP server, tool call or direct status UPDATE here.
		if err := store.ReplayProjectionDLQ(ctx, opts.request); err != nil {
			return err
		}
		action = "replay_queued"
	}
	err = json.NewEncoder(out).Encode(struct {
		Action   string                     `json:"action"`
		Eligible bool                       `json:"eligible"`
		Reason   string                     `json:"reason"`
		Observed iprojectionoutbox.DLQEntry `json:"observed"`
	}{action, reason == "eligible", reason, entry})
	if err != nil {
		return err
	}
	if opts.apply && reason != "eligible" {
		return errProjectionDLQIneligible
	}
	return nil
}

// Run executes the explicit single-ID preview/replay without starting the service.
func Run(args []string, out io.Writer) error {
	opts, err := parseProjectionDLQOptions(args)
	if errors.Is(err, flag.ErrHelp) {
		_, err = fmt.Fprintln(out, projectionDLQUsage)
		return err
	}
	if err != nil {
		return err
	}
	config, err := conf.NewCoreConfig()
	if err != nil {
		return err
	}
	if !strings.EqualFold(config.Store, "mariadb") || config.MariaDBDSN == "" {
		return errors.New("projection DLQ requires Core MariaDB")
	}
	db, err := sql.Open("mysql", config.MariaDBDSN)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return executeProjectionDLQ(ctx, sessionstore.New(db), opts, out)
}

// ErrorMessage renders only safe public diagnostics, never the database cause.
func ErrorMessage(err error) string {
	switch {
	case errors.Is(err, errProjectionDLQArguments):
		return projectionDLQUsage
	case errors.Is(err, errProjectionDLQIneligible):
		return errProjectionDLQIneligible.Error()
	case errors.Is(err, sql.ErrNoRows):
		return "projection DLQ entry not found"
	default:
		return "projection DLQ command failed; verify Core database access and current entry state"
	}
}
