// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

// record-integrity-recheck explicitly checks selected exhausted historical rounds.
// Default is read-only; --apply saves only newly computed source-version CASes.
package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/conf"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/evidencesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchevidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		if errors.Is(err, errOpenSearchAuthConfig) {
			fmt.Fprintln(os.Stderr, errOpenSearchAuthConfig)
		} else {
			fmt.Fprintln(os.Stderr, "historical record integrity recheck failed")
		}
		os.Exit(1)
	}
}
func run(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("record-integrity-recheck", flag.ContinueOnError)
	idsFile := flags.String("ids-file", "", "JSON array of explicit Interaction IDs (at most 100)")
	apply := flags.Bool("apply", false, "Atomically save newly computed verdicts for exhausted current generations")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *idsFile == "" {
		return errors.New("explicit ids-file required")
	}
	file, err := os.Open(*idsFile)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	ids, err := readIDs(file)
	if err != nil {
		return err
	}
	dsn := os.Getenv("BKN_TRACE_CORE_MARIADB_DSN")
	endpoint := os.Getenv("OPENSEARCH_ENDPOINT")
	if dsn == "" || endpoint == "" {
		return errors.New("database and OpenSearch configuration required")
	}
	auth, err := recheckOpenSearchAuth()
	if err != nil {
		return err
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(2)
	store := sessionstore.New(db)
	artifacts := opensearchevidencestore.New(opensearch.New(endpoint, auth, 25*time.Second), os.Getenv("OPENSEARCH_EVIDENCE_INDEX"))
	if os.Getenv("OPENSEARCH_EVIDENCE_INDEX") == "" {
		return errors.New("explicit evidence index required")
	}
	service := evidencesvc.New(artifacts, evidencesvc.WithSessionStore(store), evidencesvc.WithCurrentRecordIntegrity())
	failed := false
	for _, id := range ids {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		start := time.Now()
		var interaction sessionvo.Interaction
		var owner sessionvo.Owner
		eligible := false
		err = store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
			i, found := tx.PeekInteraction(id)
			if !found || i.ExecutionStatus == sessionvo.InteractionActive || i.StoredRecordIntegrity != nil {
				return errors.New("current unmaterialized terminal interaction required")
			}
			c, found := tx.PeekConversation(i.ConversationID)
			if !found {
				return errors.New("conversation unavailable")
			}
			interaction = i
			owner = c.Owner
			return nil
		})
		if err == nil {
			retry, found, e := store.GetRecordIntegrityRetry(ctx, id, interaction.IntegritySourceVersion)
			err = e
			eligible = found && retry.Status == isessionstore.RecordIntegrityExhausted
			if err == nil && !eligible {
				err = errors.New("exhausted generation required")
			}
		}
		var stored *sessionvo.StoredRecordIntegrity
		applied := false
		if err == nil {
			profile := &evidencevo.AccessProfile{ActorID: owner.EffectiveSubjectID, EffectiveSubjectID: owner.EffectiveSubjectID, ApplicationPrincipalID: owner.ApplicationPrincipalID, DelegationID: owner.DelegationID, AccountActive: true}
			stored, err = service.RecheckHistoricalRecordIntegrity(ctx, id, evidencevo.QueryScope{AccessProfile: profile})
			if err == nil && stored.SourceVersion != interaction.IntegritySourceVersion {
				stored = nil
				err = errors.New("source changed")
			}
			if err == nil && *apply {
				applied, err = store.SaveRecheckedRecordIntegrity(ctx, id, *stored)
				if err == nil && !applied {
					err = errors.New("source or retry state changed")
				}
			}
		}
		cancel()
		reason := ""
		if err != nil {
			failed = true
			reason = recheckFailureReason(err)
		}
		if e := json.NewEncoder(out).Encode(map[string]any{"interaction_id": id, "source_version": interaction.IntegritySourceVersion, "verdict": stored, "applied": applied, "error": reason, "seconds": time.Since(start).Seconds()}); e != nil {
			return e
		}
	}
	if failed {
		return errors.New("some checks did not complete")
	}
	return nil
}
func readIDs(reader io.Reader) ([]string, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, (32<<10)+1))
	if err != nil || len(raw) > 32<<10 {
		return nil, errors.New("recheck identities exceed input limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var ids []string
	if err := decoder.Decode(&ids); err != nil {
		return nil, err
	}
	if len(ids) == 0 || len(ids) > 100 {
		return nil, errors.New("invalid recheck identity count")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("trailing recheck identities")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || len(id) > 128 || strings.TrimSpace(id) != id || seen[id] {
			return nil, errors.New("invalid or duplicate recheck identity")
		}
		seen[id] = true
	}
	return ids, nil
}

var errOpenSearchAuthConfig = errors.New("OPENSEARCH_AUTH_USERNAME and OPENSEARCH_AUTH_PASSWORD are required when OPENSEARCH_AUTH_ENABLED=true")

func recheckOpenSearchAuth() (opensearch.AuthConfig, error) {
	configured := conf.NewOpenSearchConfig().Auth
	if configured.Enabled && (configured.Username == "" || configured.Password == "") {
		return opensearch.AuthConfig{}, errOpenSearchAuthConfig
	}
	return opensearch.AuthConfig{Enabled: configured.Enabled, Username: configured.Username, Password: configured.Password}, nil
}

func recheckFailureReason(err error) string {
	var upstream *opensearch.StatusError
	if errors.As(err, &upstream) && (upstream.StatusCode == http.StatusUnauthorized || upstream.StatusCode == http.StatusForbidden) {
		return "opensearch_auth_failed"
	}
	return "recheck_failed"
}
