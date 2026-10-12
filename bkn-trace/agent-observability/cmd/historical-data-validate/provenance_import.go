// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.
package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/sessionstore"
)

type historicalEEContext struct {
	InteractionID  string          `json:"interaction_id"`
	ConversationID string          `json:"conversation_id"`
	Owner          sessionvo.Owner `json:"owner"`
}
type historicalProjectionRow struct {
	InteractionID    string                `json:"interaction_id"`
	FactsHash        string                `json:"facts_hash"`
	ResolverVersion  string                `json:"resolver_version"`
	ResolvedAt       *historicalLedgerTime `json:"resolved_at"`
	Status           string                `json:"status"`
	GraphPayload     *string               `json:"graph_payload"`
	MarkdownSnapshot *string               `json:"markdown_snapshot"`
	ContentHash      *string               `json:"content_hash"`
	FailureCode      *string               `json:"failure_code"`
	CreatedAt        historicalLedgerTime  `json:"created_at"`
	UpdatedAt        historicalLedgerTime  `json:"updated_at"`
}
type historicalExplanationRow struct {
	InteractionID            string `json:"interaction_id"`
	AccessProfileFingerprint string `json:"access_profile_fingerprint"`
	InputHash                string `json:"input_hash"`
	Algorithm                string `json:"algorithm"`
	WriteToken               string `json:"write_token"`
	GeneratedAt              string `json:"generated_at"`
	ViewHash                 string `json:"view_hash"`
	ViewJSON                 string `json:"view_json"`
}
type historicalEEPlan struct {
	Contexts              []historicalEEContext      `json:"contexts"`
	HistoricalProjections []historicalProjectionRow  `json:"historical_projections"`
	Explanations          []historicalExplanationRow `json:"explanations"`
}
type historicalEEResult struct {
	Verified        bool `json:"verified"`
	Created         int  `json:"created"`
	AlreadyVerified int  `json:"already_verified"`
}

func historicalSHA256(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func historicalHash(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}
func historicalJSONIdentity(raw []byte, id string) bool {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return false
	}
	for _, key := range []string{"interaction_id", "interactionId"} {
		if field, ok := object[key]; ok {
			var actual string
			if json.Unmarshal(field, &actual) != nil || actual != id {
				return false
			}
		}
	}
	return true
}
func prepareHistoricalEE(plan historicalEEPlan) (historicalEEPlan, error) {
	contexts := map[string]historicalEEContext{}
	for _, c := range plan.Contexts {
		if c.InteractionID == "" || c.ConversationID == "" || len(c.InteractionID) > 128 || len(c.ConversationID) > 64 || c.Owner.ApplicationPrincipalID == "" || c.Owner.EffectiveSubjectType == "" || c.Owner.EffectiveSubjectID == "" {
			return plan, errors.New("invalid historical provenance context")
		}
		if prior, ok := contexts[c.InteractionID]; ok && !sameCore(prior, c) {
			return plan, errors.New("historical provenance context conflict")
		}
		contexts[c.InteractionID] = c
	}
	projections := map[string]historicalProjectionRow{}
	explanations := map[string]historicalExplanationRow{}
	output := historicalEEPlan{Contexts: plan.Contexts}
	for _, p := range plan.HistoricalProjections {
		if _, ok := contexts[p.InteractionID]; !ok {
			return plan, errors.New("historical provenance context missing")
		}
		if len(p.InteractionID) > 64 || !historicalHash(p.FactsHash) || p.ResolverVersion == "" || len(p.ResolverVersion) > 64 || p.Status == "" || len(p.Status) > 16 || time.Time(p.CreatedAt).IsZero() || time.Time(p.UpdatedAt).IsZero() || len(historicalString(p.FailureCode)) > 64 {
			return plan, errors.New("invalid historical projection")
		}
		for _, stamp := range []historicalLedgerTime{p.CreatedAt, p.UpdatedAt} {
			if !time.Time(stamp).Equal(time.Time(stamp).Truncate(time.Microsecond)) {
				return plan, errors.New("historical projection timestamp precision conflict")
			}
		}
		if p.ResolvedAt != nil && !time.Time(*p.ResolvedAt).Equal(time.Time(*p.ResolvedAt).Truncate(time.Microsecond)) {
			return plan, errors.New("historical projection timestamp precision conflict")
		}
		if p.ResolvedAt != nil && time.Time(*p.ResolvedAt).IsZero() {
			return plan, errors.New("invalid historical projection time")
		}
		if p.GraphPayload != nil && !historicalJSONIdentity([]byte(*p.GraphPayload), p.InteractionID) {
			return plan, errors.New("invalid historical projection graph")
		}
		if p.ContentHash != nil && *p.ContentHash != historicalSHA256([]byte(historicalString(p.GraphPayload)+historicalString(p.MarkdownSnapshot))) {
			return plan, errors.New("historical projection hash conflict")
		}
		if prior, ok := projections[p.InteractionID]; ok {
			if !sameCore(prior, p) {
				return plan, errors.New("historical projection duplicate conflict")
			}
			continue
		}
		projections[p.InteractionID] = p
		output.HistoricalProjections = append(output.HistoricalProjections, p)
	}
	for _, e := range plan.Explanations {
		if _, ok := contexts[e.InteractionID]; !ok {
			return plan, errors.New("historical explanation context missing")
		}
		raw, err := hex.DecodeString(strings.TrimPrefix(e.ViewJSON, "0x"))
		if err != nil || len(raw) > 16777215 || !historicalJSONIdentity(raw, e.InteractionID) || historicalSHA256(raw) != e.ViewHash {
			return plan, errors.New("historical explanation bytes or hash conflict")
		}
		if len(e.InteractionID) > 128 || len(e.AccessProfileFingerprint) > 128 || e.InputHash == "" || len(e.InputHash) > 128 || e.Algorithm == "" || len(e.Algorithm) > 128 || e.WriteToken == "" || len(e.WriteToken) > 64 || len(e.GeneratedAt) > 40 {
			return plan, errors.New("invalid historical explanation metadata")
		}
		if _, err = time.Parse(time.RFC3339Nano, e.GeneratedAt); err != nil {
			return plan, errors.New("invalid historical explanation time")
		}
		e.ViewJSON = hex.EncodeToString(raw)
		key := e.InteractionID + "\x00" + e.AccessProfileFingerprint
		if prior, ok := explanations[key]; ok {
			if !sameCore(prior, e) {
				return plan, errors.New("historical explanation duplicate conflict")
			}
			continue
		}
		explanations[key] = e
		output.Explanations = append(output.Explanations, e)
	}
	return output, nil
}
func importHistoricalEE(reader io.Reader, writer io.Writer) error {
	return historicalEECommand(reader, writer, false)
}
func validateHistoricalEE(reader io.Reader, writer io.Writer) error {
	return historicalEECommand(reader, writer, true)
}
func historicalEECommand(reader io.Reader, writer io.Writer, validateOnly bool) (failure error) {
	stage := "input"
	defer func() {
		if failure != nil {
			_ = json.NewEncoder(writer).Encode(map[string]any{"verified": false, "reason": "native_provenance_import_failed_" + stage})
		}
	}()
	var plan historicalEEPlan
	decoder := json.NewDecoder(io.LimitReader(reader, 128<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("unexpected historical provenance input")
	}
	stage = "validation"
	plan, err := prepareHistoricalEE(plan)
	if err != nil {
		return err
	}
	if validateOnly {
		return json.NewEncoder(writer).Encode(historicalEEResult{Verified: true})
	}
	stage = "configuration"
	dsn := os.Getenv("BKN_TRACE_CORE_MARIADB_DSN")
	if dsn == "" {
		return errors.New("core database configuration missing")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return errors.New("core database unavailable")
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	stage = "schema"
	if err = sessionstore.New(db).EnsureSchema(ctx, false); err != nil {
		return errors.New("core schema check failed")
	}
	stage = "transaction"
	result, err := importHistoricalEEPlan(ctx, db, plan)
	if err != nil {
		return err
	}
	return json.NewEncoder(writer).Encode(result)
}
func readHistoricalEE(ctx context.Context, q historicalLedgerQuery, table string, columns []string, identity []any, lock bool, target any) (bool, error) {
	parts := make([]string, 0, len(columns)*2)
	for _, column := range columns {
		value := column
		switch column {
		case "resolved_at", "created_at", "updated_at":
			value = "DATE_FORMAT(" + column + ",'%Y-%m-%d %H:%i:%s.%f')"
		case "view_json":
			value = "LOWER(HEX(view_json))"
		}
		parts = append(parts, "'"+column+"'", value)
	}
	statement := "SELECT JSON_OBJECT(" + strings.Join(parts, ",") + ") FROM " + table + " WHERE interaction_id=?"
	if len(identity) == 2 {
		statement += " AND access_profile_fingerprint=?"
	}
	if lock {
		statement += " FOR UPDATE"
	}
	var raw []byte
	if err := q.QueryRowContext(ctx, statement, identity...).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return false, err
	}
	return true, nil
}

var historicalProjectionColumns = []string{"interaction_id", "facts_hash", "resolver_version", "resolved_at", "status", "graph_payload", "markdown_snapshot", "content_hash", "failure_code", "created_at", "updated_at"}
var historicalExplanationColumns = []string{"interaction_id", "access_profile_fingerprint", "input_hash", "algorithm", "write_token", "generated_at", "view_hash", "view_json"}

func importHistoricalEEPlan(ctx context.Context, db *sql.DB, source historicalEEPlan) (historicalEEResult, error) {
	result := historicalEEResult{}
	plan, err := prepareHistoricalEE(source)
	if err != nil {
		return result, err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	used := map[string]bool{}
	for _, p := range plan.HistoricalProjections {
		used[p.InteractionID] = true
	}
	for _, e := range plan.Explanations {
		used[e.InteractionID] = true
	}
	for _, c := range plan.Contexts {
		if !used[c.InteractionID] {
			continue
		}
		var owner sessionvo.Owner
		if err = tx.QueryRowContext(ctx, `SELECT c.application_principal_id,c.effective_subject_type,c.effective_subject_id,c.delegation_id FROM bkn_trace_conversations c JOIN bkn_trace_interactions i ON i.conversation_id=c.conversation_id WHERE c.conversation_id=? AND i.interaction_id=? FOR UPDATE`, c.ConversationID, c.InteractionID).Scan(&owner.ApplicationPrincipalID, &owner.EffectiveSubjectType, &owner.EffectiveSubjectID, &owner.DelegationID); err != nil {
			return historicalEEResult{}, err
		}
		if !owner.Equal(c.Owner) {
			return historicalEEResult{}, errors.New("historical provenance core owner conflict")
		}
	}
	restore := func(table string, columns []string, identity []any, row any, values []any) error {
		target := new(historicalProjectionRow)
		var dest any = target
		if table == "bkn_trace_ee_current_explanations" {
			dest = new(historicalExplanationRow)
		}
		found, e := readHistoricalEE(ctx, tx, table, columns, identity, true, dest)
		if e != nil {
			return e
		}
		if found {
			if !sameCore(row, dest) {
				return errors.New("historical provenance target content conflict")
			}
			result.AlreadyVerified++
			return nil
		}
		statement := "INSERT INTO " + table + " (" + strings.Join(columns, ",") + ") VALUES (" + strings.TrimSuffix(strings.Repeat("?,", len(columns)), ",") + ")"
		if _, e = tx.ExecContext(ctx, statement, values...); e != nil {
			return e
		}
		found, e = readHistoricalEE(ctx, tx, table, columns, identity, true, dest)
		if e != nil {
			return e
		}
		if !found || !sameCore(row, dest) {
			return errors.New("historical provenance readback conflict")
		}
		result.Created++
		return nil
	}
	for _, p := range plan.HistoricalProjections {
		var resolved any
		if p.ResolvedAt != nil {
			resolved = time.Time(*p.ResolvedAt)
		}
		if err = restore("bkn_trace_ee_historical_provenance_projections", historicalProjectionColumns, []any{p.InteractionID}, p, []any{p.InteractionID, p.FactsHash, p.ResolverVersion, resolved, p.Status, p.GraphPayload, p.MarkdownSnapshot, p.ContentHash, p.FailureCode, time.Time(p.CreatedAt), time.Time(p.UpdatedAt)}); err != nil {
			return historicalEEResult{}, err
		}
	}
	for _, e := range plan.Explanations {
		raw, _ := hex.DecodeString(e.ViewJSON)
		if err = restore("bkn_trace_ee_current_explanations", historicalExplanationColumns, []any{e.InteractionID, e.AccessProfileFingerprint}, e, []any{e.InteractionID, e.AccessProfileFingerprint, e.InputHash, e.Algorithm, e.WriteToken, e.GeneratedAt, e.ViewHash, raw}); err != nil {
			return historicalEEResult{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return historicalEEResult{}, err
	}
	for _, p := range plan.HistoricalProjections {
		var stored historicalProjectionRow
		found, e := readHistoricalEE(ctx, db, "bkn_trace_ee_historical_provenance_projections", historicalProjectionColumns, []any{p.InteractionID}, false, &stored)
		if e != nil {
			return result, e
		}
		if !found || !sameCore(p, stored) {
			return result, errors.New("historical projection persisted readback conflict")
		}
	}
	for _, row := range plan.Explanations {
		var stored historicalExplanationRow
		found, e := readHistoricalEE(ctx, db, "bkn_trace_ee_current_explanations", historicalExplanationColumns, []any{row.InteractionID, row.AccessProfileFingerprint}, false, &stored)
		if e != nil {
			return result, e
		}
		if !found || !sameCore(row, stored) {
			return result, errors.New("historical explanation persisted readback conflict")
		}
	}
	result.Verified = true
	return result, nil
}
