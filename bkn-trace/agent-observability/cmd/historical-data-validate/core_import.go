// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

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
	Conversations      []sessionvo.Conversation      `json:"conversations"`
	Interactions       []sessionvo.Interaction       `json:"interactions"`
	Operations         []sessionvo.Operation         `json:"operations"`
	Receipts           []sessionvo.Receipt           `json:"receipts"`
	CallFacts          []sessionvo.OperationCallFact `json:"call_facts"`
	PreviousReceipts   []sessionvo.Receipt           `json:"previous_receipts,omitempty"`
	PreviousCallFacts  []sessionvo.OperationCallFact `json:"previous_call_facts,omitempty"`
	IdempotencyRecords []coreIdempotencyRecord       `json:"idempotency_records,omitempty"`
	AssemblyRevisions  []sessionvo.AssemblyRevision  `json:"assembly_revisions,omitempty"`
}
type coreImportResult struct {
	Verified        bool `json:"verified"`
	Created         int  `json:"created"`
	Updated         int  `json:"updated"`
	AlreadyVerified int  `json:"already_verified"`
}

// This is an offline conversion writer. All rows use the existing native store,
// including its record-integrity calculation; no online migration branch exists.
func importCoreRecords(reader io.Reader, writer io.Writer, validateOnly bool) (failure error) {
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
	stage = "validation"
	if err := prepareCorePlan(&plan); err != nil {
		return err
	}
	if validateOnly {
		return json.NewEncoder(writer).Encode(coreImportResult{Verified: true})
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
		validStatus := v.Status == sessionvo.ConversationActive || v.Status == sessionvo.ConversationClosed || v.Status == sessionvo.ConversationExpired
		if !validID(v.ID) || convs[v.ID].ID != "" || v.Owner.EffectiveSubjectID == "" || v.Owner.ApplicationPrincipalID == "" || v.CreatedAt.IsZero() || v.UpdatedAt.Before(v.CreatedAt) || !validStatus {
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
		pending := v.Status == sessionvo.ReceiptPending
		terminal := v.Status == sessionvo.ReceiptCompleted || v.Status == sessionvo.ReceiptFailed
		if !validID(v.ID) || receipts[v.ID].ID != "" || o.ID == "" || o.InteractionID != v.InteractionID || o.ConversationID != v.ConversationID || !v.Owner.Equal(c.Owner) || v.Attempt == 0 || (!pending && !terminal) || (terminal && v.TerminalAt == nil) || (pending && v.TerminalAt != nil) {
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
		if seen[key] || r.ID == "" || r.OperationID != v.OperationID || r.Attempt != v.Attempt || r.InteractionID != v.InteractionID || r.ConversationID != v.ConversationID || r.RequestID != v.RequestID || r.TraceID != v.TraceID || !v.Protocol.IsValid() || (r.Status != sessionvo.ReceiptPending && v.FinishedAt == nil) {
			return fmt.Errorf("invalid converted call fact")
		}
		seen[key] = true
	}
	for _, v := range p.IdempotencyRecords {
		conversationID := v.ResourceID
		if v.ResourceType == "interaction" {
			conversationID = ints[v.ResourceID].ConversationID
		}
		c := convs[conversationID]
		if v.Scope == "" || v.IdempotencyKey == "" || v.CreatedAt.IsZero() || c.ID == "" || !v.Owner.Equal(c.Owner) {
			return fmt.Errorf("invalid historical idempotency record")
		}
	}
	for _, v := range p.AssemblyRevisions {
		if !validID(v.ID) || ints[v.InteractionID].ID == "" || v.RevisionNo == 0 || v.CreatedAt.IsZero() {
			return fmt.Errorf("invalid historical assembly revision")
		}
	}
	return nil
}
func sameCore(left, right any) bool {
	// JSON excludes native store's derived integrity metadata. Decode to normalize
	// JSON object key ordering and time locations without weakening field checks.
	if v, ok := left.(sessionvo.Interaction); ok {
		left = interactionRecord(v)
	}
	if v, ok := right.(sessionvo.Interaction); ok {
		right = interactionRecord(v)
	}
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
	if err := prepareCorePlan(&p); err != nil {
		return result, err
	}

	// The native store bounds integrity invalidation per transaction. Partition
	// by interaction while keeping each operation/receipt/call together.
	if len(p.Interactions) > 100 {
		countedConversations := map[string]bool{}
		countedIdempotency := map[string]bool{}
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
			for _, v := range p.PreviousReceipts {
				if ids[v.InteractionID] {
					chunk.PreviousReceipts = append(chunk.PreviousReceipts, v)
				}
			}
			for _, v := range p.PreviousCallFacts {
				if ids[v.InteractionID] {
					chunk.PreviousCallFacts = append(chunk.PreviousCallFacts, v)
				}
			}
			for _, v := range p.IdempotencyRecords {
				if (v.ResourceType == "interaction" && ids[v.ResourceID]) || (v.ResourceType == "conversation" && convs[v.ResourceID]) {
					chunk.IdempotencyRecords = append(chunk.IdempotencyRecords, v)
				}
			}
			for _, v := range p.AssemblyRevisions {
				if ids[v.InteractionID] {
					chunk.AssemblyRevisions = append(chunk.AssemblyRevisions, v)
				}
			}
			part, err := importCorePlan(ctx, store, chunk)
			if err != nil {
				return result, err
			}
			result.Created += part.Created
			result.Updated += part.Updated
			result.AlreadyVerified += part.AlreadyVerified
			for _, v := range chunk.Conversations {
				if countedConversations[v.ID] {
					result.AlreadyVerified--
				}
				countedConversations[v.ID] = true
			}
			for _, v := range chunk.IdempotencyRecords {
				key := v.Scope + "\x00" + v.Owner.Key() + "\x00" + v.ExternalConversationKey + "\x00" + v.IdempotencyKey
				if countedIdempotency[key] {
					result.AlreadyVerified--
				}
				countedIdempotency[key] = true
			}
		}
		// Empty historical conversations are records too. They must not vanish
		// merely because transaction batching follows interaction dependencies.
		empty := coreImportPlan{}
		for _, v := range p.Conversations {
			if !countedConversations[v.ID] {
				empty.Conversations = append(empty.Conversations, v)
			}
		}
		for _, v := range p.IdempotencyRecords {
			if v.ResourceType == "conversation" && !countedConversations[v.ResourceID] {
				empty.IdempotencyRecords = append(empty.IdempotencyRecords, v)
			}
		}
		if len(empty.Conversations) > 0 {
			part, err := importCorePlan(ctx, store, empty)
			if err != nil {
				return result, err
			}
			result.Created += part.Created
			result.Updated += part.Updated
			result.AlreadyVerified += part.AlreadyVerified
		}
		result.Verified = true
		return result, nil
	}
	previousReceipts := map[string]sessionvo.Receipt{}
	for _, v := range p.PreviousReceipts {
		previousReceipts[v.ID] = v
	}
	previousCalls := map[string]sessionvo.OperationCallFact{}
	for _, v := range p.PreviousCallFacts {
		previousCalls[coreCallKey(v)] = v
	}
	apply := func(tx isessionstore.Transaction, write bool) error {
		check := func(found bool, old, next, previous any, save func()) error {
			if found {
				if !sameCore(old, next) {
					if !write || previous == nil || !sameCore(old, previous) {
						return fmt.Errorf("core target content conflict")
					}
					save()
					result.Updated++
					return nil
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
			if err := check(found, old, v, nil, func() { tx.SaveConversation(v) }); err != nil {
				return err
			}
		}
		for _, v := range p.Interactions {
			old, found := tx.PeekInteraction(v.ID)
			if err := check(found, old, v, nil, func() { tx.SaveInteraction(v) }); err != nil {
				return err
			}
		}
		for _, v := range p.Operations {
			old, found := tx.PeekOperation(v.ID)
			if err := check(found, old, v, nil, func() { tx.SaveOperation(v) }); err != nil {
				return err
			}
		}
		for _, v := range p.Receipts {
			old, found := tx.FindReceipt(v.ID)
			var previous any
			if prior, exists := previousReceipts[v.ID]; exists {
				previous = prior
			}
			if err := check(found, old, v, previous, func() { tx.SaveReceipt(v) }); err != nil {
				return err
			}
		}
		for _, v := range p.CallFacts {
			old, found := tx.FindOperationCallFact(v.OperationID, v.Attempt)
			var previous any
			if prior, exists := previousCalls[coreCallKey(v)]; exists {
				previous = prior
			}
			// MariaDB currently preserves parent_operation_id on existing-row
			// updates. The strict readback below must reject an unapplied parent
			// repair; a successful memory-store test is not database qualification.
			if err := check(found, old, v, previous, func() { tx.SaveOperationCallFact(v) }); err != nil {
				return err
			}
		}
		for _, wire := range p.IdempotencyRecords {
			v := wire.native()
			old, found := tx.FindIdempotency(v.Scope, v.Owner, v.ExternalConversationKey, v.IdempotencyKey)
			if err := check(found, old, v, nil, func() { tx.SaveIdempotency(v) }); err != nil {
				return err
			}
		}
		for _, v := range p.AssemblyRevisions {
			var old sessionvo.AssemblyRevision
			found := false
			for _, prior := range tx.ListAssemblyRevisions(v.InteractionID) {
				if prior.ID == v.ID {
					old, found = prior, true
					break
				}
			}
			if err := check(found, old, v, nil, func() { tx.SaveAssemblyRevision(v) }); err != nil {
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

// Use the same native validation and payload normalization before any write,
// including before transaction partitioning and in validation-only mode.
func prepareCorePlan(p *coreImportPlan) error {
	if err := validateCorePlan(*p); err != nil {
		return err
	}
	for i := range p.Interactions {
		if p.Interactions[i].StartIdempotencyKey == "" {
			p.Interactions[i].StartIdempotencyKey = p.Interactions[i].ID
		}
	}
	calls := make([]*sessionvo.OperationCallFact, 0, len(p.CallFacts)+len(p.PreviousCallFacts))
	for i := range p.CallFacts {
		calls = append(calls, &p.CallFacts[i])
	}
	for i := range p.PreviousCallFacts {
		calls = append(calls, &p.PreviousCallFacts[i])
	}
	for _, v := range calls {
		var err error
		v.Input, err = sessionvo.NormalizePayloadEnvelope(v.Input)
		if err != nil {
			return fmt.Errorf("invalid converted input")
		}
		for _, payload := range []*sessionvo.PayloadEnvelope{v.Output, v.Error} {
			if payload != nil {
				normalized, e := sessionvo.NormalizePayloadEnvelope(*payload)
				if e != nil {
					return fmt.Errorf("invalid converted payload")
				}
				*payload = normalized
			}
		}
	}
	return validateCorePriors(*p)
}

func coreCallKey(v sessionvo.OperationCallFact) string {
	return fmt.Sprintf("%s:%d", v.OperationID, v.Attempt)
}

// Prior records are exact frozen converter output, never a general overwrite
// allowance. Only TraceID and a missing call parent may differ from the target.
func validateCorePriors(p coreImportPlan) error {
	receipts := map[string]sessionvo.Receipt{}
	for _, v := range p.Receipts {
		receipts[v.ID] = v
	}
	seen := map[string]bool{}
	for _, prior := range p.PreviousReceipts {
		next, found := receipts[prior.ID]
		if !found || seen[prior.ID] || prior.TraceID == "" {
			return fmt.Errorf("invalid converted prior receipt")
		}
		seen[prior.ID] = true
		prior.TraceID = next.TraceID
		if !sameCore(prior, next) {
			return fmt.Errorf("invalid converted prior receipt")
		}
	}
	calls := map[string]sessionvo.OperationCallFact{}
	for _, v := range p.CallFacts {
		calls[coreCallKey(v)] = v
	}
	seen = map[string]bool{}
	for _, prior := range p.PreviousCallFacts {
		key := coreCallKey(prior)
		next, found := calls[key]
		if !found || seen[key] || prior.TraceID == "" || (prior.ParentOperationID != "" && prior.ParentOperationID != next.ParentOperationID) {
			return fmt.Errorf("invalid converted prior call fact")
		}
		seen[key] = true
		prior.TraceID, prior.ParentOperationID = next.TraceID, next.ParentOperationID
		if !sameCore(prior, next) {
			return fmt.Errorf("invalid converted prior call fact")
		}
	}
	return nil
}
