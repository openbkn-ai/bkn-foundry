// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN

package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-sql-driver/mysql"
	"io"
	"os"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

type coreImportPlan struct {
	Conversations []sessionvo.Conversation      `json:"conversations"`
	Interactions  []sessionvo.Interaction       `json:"interactions"`
	Operations    []sessionvo.Operation         `json:"operations"`
	Receipts      []sessionvo.Receipt           `json:"receipts"`
	CallFacts     []sessionvo.OperationCallFact `json:"call_facts"`
}
type coreImportResult struct {
	Verified        bool `json:"verified"`
	Created         int  `json:"created"`
	AlreadyVerified int  `json:"already_verified"`
}

// This is an offline conversion writer. All rows use the existing native store,
// including its record-integrity calculation; no online migration branch exists.
func importCoreRecords(reader io.Reader, writer io.Writer) (failure error) {
	stage := "input"
	defer func() {
		if failure != nil {
			reason := "native_core_import_failed_" + stage
			for _, known := range []string{"core target content conflict", "core readback missing", "invalid converted input", "invalid converted payload", "invalid converted receipt", "invalid converted business reference"} {
				if failure.Error() == known {
					reason = known
				}
			}
			var databaseError *mysql.MySQLError
			if errors.As(failure, &databaseError) {
				reason = fmt.Sprintf("native_core_database_error_%d", databaseError.Number)
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"verified": false, "reason": reason})
		}
	}()

	var plan coreImportPlan
	decoder := json.NewDecoder(io.LimitReader(reader, 128<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil {
		return fmt.Errorf("invalid Core import plan: %v", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("unexpected Core import input")
	}
	stage = "configuration"
	dsn := os.Getenv("BKN_TRACE_CORE_MARIADB_DSN")
	if dsn == "" {
		return fmt.Errorf("core database configuration missing")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("core database unavailable")
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	stage = "schema"
	store := sessionstore.New(db)
	if err := store.EnsureSchema(ctx, false); err != nil {
		return fmt.Errorf("core schema check failed")
	}
	stage = "transaction"
	result, err := importCorePlan(ctx, store, plan)
	if err != nil {
		return err
	}
	return json.NewEncoder(writer).Encode(result)
}

func validateCorePlan(p coreImportPlan) error {
	convs := map[string]sessionvo.Conversation{}
	ints := map[string]sessionvo.Interaction{}
	ops := map[string]sessionvo.Operation{}
	receipts := map[string]sessionvo.Receipt{}
	validID := func(id string) bool { return id != "" && len(id) <= 64 }
	for _, v := range p.Conversations {
		if !validID(v.ID) || convs[v.ID].ID != "" || v.Owner.EffectiveSubjectID == "" || v.Owner.ApplicationPrincipalID == "" || v.CreatedAt.IsZero() || v.UpdatedAt.Before(v.CreatedAt) || v.Status != sessionvo.ConversationClosed {
			return fmt.Errorf("invalid converted conversation")
		}
		convs[v.ID] = v
	}
	for _, v := range p.Interactions {
		if !validID(v.ID) || ints[v.ID].ID != "" || convs[v.ConversationID].ID == "" || v.Ordinal == 0 || v.CreatedAt.IsZero() || v.TerminalAt == nil || !v.IsTerminal() {
			return fmt.Errorf("invalid converted interaction")
		}
		ints[v.ID] = v
	}
	for _, v := range p.Operations {
		i := ints[v.InteractionID]
		if !validID(v.ID) || ops[v.ID].ID != "" || i.ID == "" || i.ConversationID != v.ConversationID || v.Attempt == 0 || v.ToolName == "" || v.CreatedAt.IsZero() {
			return fmt.Errorf("invalid converted operation")
		}
		ops[v.ID] = v
	}
	for _, v := range p.Receipts {
		o := ops[v.OperationID]
		c := convs[v.ConversationID]
		if !validID(v.ID) || receipts[v.ID].ID != "" || o.ID == "" || o.InteractionID != v.InteractionID || o.ConversationID != v.ConversationID || !v.Owner.Equal(c.Owner) || v.RequestID == "" || v.TraceID == "" || v.Attempt == 0 || v.TerminalAt == nil || (v.Status != sessionvo.ReceiptCompleted && v.Status != sessionvo.ReceiptFailed) {
			return fmt.Errorf("invalid converted receipt")
		}
		for _, ref := range v.BusinessRefs {
			if !ref.IsCanonical() {
				return fmt.Errorf("invalid converted business reference")
			}
		}
		receipts[v.ID] = v
	}
	seen := map[string]bool{}
	for _, v := range p.CallFacts {
		r := receipts[v.ReceiptID]
		key := fmt.Sprintf("%s:%d", v.OperationID, v.Attempt)
		if seen[key] || r.ID == "" || r.OperationID != v.OperationID || r.Attempt != v.Attempt || r.InteractionID != v.InteractionID || r.ConversationID != v.ConversationID || r.RequestID != v.RequestID || r.TraceID != v.TraceID || !v.Protocol.IsValid() || v.FinishedAt == nil {
			return fmt.Errorf("invalid converted call fact")
		}
		seen[key] = true
	}
	return nil
}
func sameCore(left, right any) bool {
	// JSON excludes native store's derived integrity metadata. Decode to normalize
	// JSON object key ordering and time locations without weakening field checks.
	a, _ := json.Marshal(left)
	b, _ := json.Marshal(right)
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	a, _ = json.Marshal(x)
	b, _ = json.Marshal(y)
	return bytes.Equal(a, b)
}
func importCorePlan(ctx context.Context, store isessionstore.Store, p coreImportPlan) (coreImportResult, error) {
	result := coreImportResult{}
	if err := validateCorePlan(p); err != nil {
		return result, err
	}

	// The native store bounds integrity invalidation per transaction. Partition
	// by interaction while keeping each operation/receipt/call together.
	if len(p.Interactions) > 100 {
		countedConversations := map[string]bool{}
		for start := 0; start < len(p.Interactions); start += 100 {
			end := start + 100
			if end > len(p.Interactions) {
				end = len(p.Interactions)
			}
			chunk := coreImportPlan{Interactions: p.Interactions[start:end]}
			ids, convs := map[string]bool{}, map[string]bool{}
			for _, v := range chunk.Interactions {
				ids[v.ID] = true
				convs[v.ConversationID] = true
			}
			for _, v := range p.Conversations {
				if convs[v.ID] {
					chunk.Conversations = append(chunk.Conversations, v)
				}
			}
			for _, v := range p.Operations {
				if ids[v.InteractionID] {
					chunk.Operations = append(chunk.Operations, v)
				}
			}
			for _, v := range p.Receipts {
				if ids[v.InteractionID] {
					chunk.Receipts = append(chunk.Receipts, v)
				}
			}
			for _, v := range p.CallFacts {
				if ids[v.InteractionID] {
					chunk.CallFacts = append(chunk.CallFacts, v)
				}
			}
			part, err := importCorePlan(ctx, store, chunk)
			if err != nil {
				return result, err
			}
			result.Created += part.Created
			result.AlreadyVerified += part.AlreadyVerified
			for _, v := range chunk.Conversations {
				if countedConversations[v.ID] {
					result.AlreadyVerified--
				}
				countedConversations[v.ID] = true
			}
		}
		result.Verified = true
		return result, nil
	}
	for i := range p.Interactions {
		p.Interactions[i].StartIdempotencyKey = p.Interactions[i].ID
	}
	for i := range p.CallFacts {
		v := &p.CallFacts[i]
		var err error
		v.Input, err = sessionvo.NormalizePayloadEnvelope(v.Input)
		if err != nil {
			return result, fmt.Errorf("invalid converted input")
		}
		for _, payload := range []*sessionvo.PayloadEnvelope{v.Output, v.Error} {
			if payload != nil {
				normalized, e := sessionvo.NormalizePayloadEnvelope(*payload)
				if e != nil {
					return result, fmt.Errorf("invalid converted payload")
				}
				*payload = normalized
			}
		}
	}
	apply := func(tx isessionstore.Transaction, write bool) error {
		check := func(found bool, old, next any, save func()) error {
			if found {
				if !sameCore(old, next) {
					return fmt.Errorf("core target content conflict")
				}
				if write {
					result.AlreadyVerified++
				}
				return nil
			}
			if !write {
				return fmt.Errorf("core readback missing")
			}
			save()
			result.Created++
			return nil
		}
		for _, v := range p.Conversations {
			old, found := tx.PeekConversation(v.ID)
			if err := check(found, old, v, func() { tx.SaveConversation(v) }); err != nil {
				return err
			}
		}
		for _, v := range p.Interactions {
			old, found := tx.PeekInteraction(v.ID)
			if err := check(found, old, v, func() { tx.SaveInteraction(v) }); err != nil {
				return err
			}
		}
		for _, v := range p.Operations {
			old, found := tx.PeekOperation(v.ID)
			if err := check(found, old, v, func() { tx.SaveOperation(v) }); err != nil {
				return err
			}
		}
		for _, v := range p.Receipts {
			old, found := tx.PeekReceipt(v.ID)
			if err := check(found, old, v, func() { tx.SaveReceipt(v) }); err != nil {
				return err
			}
		}
		for _, v := range p.CallFacts {
			old, found := tx.FindOperationCallFact(v.OperationID, v.Attempt)
			if err := check(found, old, v, func() { tx.SaveOperationCallFact(v) }); err != nil {
				return err
			}
		}
		return nil
	}
	if err := store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error { result = coreImportResult{}; return apply(tx, true) }); err != nil {
		return coreImportResult{}, err
	}
	if err := store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error { return apply(tx, false) }); err != nil {
		return result, err
	}
	result.Verified = true
	return result, nil
}
