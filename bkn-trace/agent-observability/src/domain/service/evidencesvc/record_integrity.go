// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iartifactstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

type recordIntegrityBudgetKey struct{}
type recordIntegrityReadBudget struct {
	reads int
	bytes int64
}

func withRecordIntegrityReadBudget(ctx context.Context) context.Context {
	if _, ok := ctx.Value(recordIntegrityBudgetKey{}).(*recordIntegrityReadBudget); ok {
		return ctx
	}
	return context.WithValue(ctx, recordIntegrityBudgetKey{}, &recordIntegrityReadBudget{})
}

type payloadVerifier func(sessionvo.OperationCallFact, string, sessionvo.PayloadEnvelope) (json.RawMessage, error)

func evaluateRecordIntegrity(snapshot sessionvo.EvidenceSnapshot, owner sessionvo.Owner, now time.Time, verify payloadVerifier) (*evidencevo.RecordIntegrity, error) {
	interaction := snapshot.Interaction
	if interaction.ExecutionStatus == sessionvo.InteractionActive {
		return nil, nil
	}
	if !hasRecordIntegrityCalls(snapshot) {
		return nil, nil
	}

	report := &evidencevo.RecordIntegrity{Status: "complete", CheckedAt: now.UTC(), Scope: "registered_call_records", Missing: []evidencevo.MissingRecord{}}
	add := func(f sessionvo.OperationCallFact, reason, field string) {
		report.Missing = append(report.Missing, evidencevo.MissingRecord{OperationID: f.OperationID, Attempt: f.Attempt, ToolName: f.ToolName, Reason: reason, Field: field})
	}
	explicitGap := false
	if interaction.ClosureManifest != nil {
		for _, reason := range interaction.ClosureManifest.SystemPartialReasons {
			if tool, requestID, ok := sessionvo.ParseTraceCallGap(reason); ok {
				explicitGap = true
				add(sessionvo.OperationCallFact{ToolName: tool}, "call_outcome_missing", "request_id:"+requestID)
			}
		}
	}
	payloads := map[string]json.RawMessage{}
	check := func(f sessionvo.OperationCallFact, field string, p *sessionvo.PayloadEnvelope) error {
		if p == nil || p.Mode == "" || p.Mode == sessionvo.PayloadOmitted {
			add(f, "record_content_missing", field)
			return nil
		}
		if p.Mode == sessionvo.PayloadInline {
			if len(p.Inline) == 0 || !json.Valid(p.Inline) {
				return errors.New("invalid stored inline call payload")
			}
			if field == "input" || field == "error" {
				payloads[fmt.Sprintf("%s:%d:%s", f.OperationID, f.Attempt, field)] = p.Inline
			}
			return nil
		}
		if p.Mode != sessionvo.PayloadReferenced {
			return errors.New("unknown stored payload mode")
		}
		if verify == nil {
			return errors.New("record integrity artifact verifier unavailable")
		}
		raw, err := verify(f, field, *p)
		if err != nil {
			return err
		}
		if raw == nil {
			add(f, "record_content_missing", field)
		} else if field == "input" || field == "error" {
			payloads[fmt.Sprintf("%s:%d:%s", f.OperationID, f.Attempt, field)] = raw
		}
		return nil
	}
	type key struct {
		id      string
		attempt uint32
	}
	facts := map[key]sessionvo.OperationCallFact{}
	receipts := map[key]sessionvo.Receipt{}
	for _, r := range snapshot.Receipts {
		if !r.Owner.Equal(owner) || r.InteractionID != interaction.ID || r.ConversationID != interaction.ConversationID {
			return nil, errors.New("receipt integrity identity mismatch")
		}
		k := key{r.OperationID, r.Attempt}
		if _, duplicate := receipts[k]; duplicate {
			return nil, errors.New("duplicate receipt attempt")
		}
		receipts[k] = r
	}
	events := []ledgervo.Event{}
	if snapshot.Ledger != nil {
		for _, row := range snapshot.Ledger.Events {
			var e ledgervo.Event
			if err := json.Unmarshal(row.Envelope, &e); err != nil {
				return nil, err
			}
			if e.EventID != row.EventID {
				return nil, errors.New("ledger integrity identity mismatch")
			}
			events = append(events, e)
		}
	}
	for _, f := range snapshot.CallFacts {
		if f.InteractionID != interaction.ID || f.ConversationID != interaction.ConversationID {
			return nil, errors.New("call integrity identity mismatch")
		}
		k := key{f.OperationID, f.Attempt}
		if _, duplicate := facts[k]; duplicate {
			return nil, errors.New("duplicate registered call attempt")
		}
		facts[k] = f
		r, hasReceipt := receipts[k]
		opFound := false
		for _, op := range snapshot.Operations {
			opFound = opFound || op.ID == f.OperationID
		}
		if !opFound {
			add(f, "record_content_missing", "operation")
		}

		terminal := f.Status == sessionvo.AttemptCompleted || f.Status == sessionvo.AttemptFailed
		if !terminal {
			add(f, "call_outcome_missing", "outcome")
		}
		if err := check(f, "input", &f.Input); err != nil {
			return nil, err
		}
		policyOmitted := hasReceipt && slices.Contains(r.PartialReasons, "not_collected_due_to_policy")
		if f.Status == sessionvo.AttemptCompleted && (!policyOmitted || !payloadOmitted(f.Output)) {
			if err := check(f, "output", f.Output); err != nil {
				return nil, err
			}
		}
		if f.Status == sessionvo.AttemptFailed && (!policyOmitted || !payloadOmitted(f.Error)) {
			if err := check(f, "error", f.Error); err != nil {
				return nil, err
			}
		}
		if !hasReceipt {
			add(f, "record_content_missing", "receipt")
		} else if r.ID != f.ReceiptID || r.RequestID != f.RequestID || r.TraceID != f.TraceID {
			return nil, errors.New("receipt call identity mismatch")
		}
		if hasReceipt && terminal && (r.Status == sessionvo.ReceiptPending || string(r.Status) != string(f.Status)) {
			add(f, "call_outcome_missing", "receipt.outcome")
		}
		profile := f.CapabilityProfile
		if profile == nil || profile.Resolution != "matched" {
			continue
		}
		if slices.Contains(profile.RequiredTraceFields, "business_refs") {
			recorded, err := integrityBusinessTargetRecorded(f, r, events, owner, payloads[fmt.Sprintf("%s:%d:input", f.OperationID, f.Attempt)], payloads[fmt.Sprintf("%s:%d:error", f.OperationID, f.Attempt)])
			if err != nil {
				return nil, err
			}
			if !recorded {
				add(f, "business_target_missing", "business_refs")
			}
		}
		if f.Status != sessionvo.AttemptCompleted || !slices.Contains(profile.RequiredTraceFields, "result_completeness") {
			continue
		}
		if profile.EvidenceContract == "managed_function_execution/v1" {
			continue
		}
		// A trusted policy stop may omit this call's new result ledger only when
		// the result itself was intentionally omitted and no pre-existing event
		// reference claims that an event should already exist.
		if policyOmitted && payloadOmitted(f.Output) && (!hasReceipt || len(r.ObservedEvidenceRefs) == 0) {
			continue
		}
		if snapshot.Ledger == nil {
			return nil, errors.New("record integrity ledger was not read")
		}
		eventType := integrityEventType(profile.EvidenceContract)
		if eventType == "" {
			return nil, fmt.Errorf("record integrity contract unsupported: %s", profile.EvidenceContract)
		}
		found := false
		for _, e := range events {
			if e.EventType != eventType || !e.Owner.Equal(owner) || e.ConversationID != interaction.ConversationID || e.InteractionID != interaction.ID || e.OperationID != f.OperationID || e.Attempt != f.Attempt || e.RequestID != f.RequestID || e.TraceID != f.TraceID {
				continue
			}
			var envelope struct {
				Payload map[string]json.RawMessage `json:"payload"`
			}
			if err := json.Unmarshal(e.Envelope, &envelope); err != nil {
				return nil, err
			}
			field := "truncated"
			if eventType == "ontology.schema.snapshot" || eventType == "metric.query.completed" {
				field = "complete"
			}
			if v, ok := envelope.Payload[field]; ok && (string(v) == "true" || string(v) == "false") {
				found = true
			}
		}
		if !found {
			add(f, "record_content_missing", "evidence.result_completeness")
		}
	}
	for _, r := range snapshot.Receipts {
		opFound := false
		for _, op := range snapshot.Operations {
			opFound = opFound || op.ID == r.OperationID
		}
		if !opFound {
			return nil, errors.New("receipt references an unregistered operation")
		}
		for _, ref := range r.ArtifactRefs {
			f := sessionvo.OperationCallFact{OperationID: r.OperationID, Attempt: r.Attempt, ToolName: r.ToolName, RequestID: r.RequestID, TraceID: r.TraceID}
			p := sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadReferenced, Ref: ref}
			if err := check(f, "artifact", &p); err != nil {
				return nil, err
			}
		}
		for _, ref := range r.ObservedEvidenceRefs {
			if snapshot.Ledger == nil {
				return nil, errors.New("record integrity ledger was not read")
			}
			found := false
			for _, e := range events {
				if e.EventID == ref && e.Owner.Equal(owner) && e.InteractionID == interaction.ID && e.ConversationID == interaction.ConversationID && e.OperationID == r.OperationID && e.Attempt == r.Attempt && e.RequestID == r.RequestID && e.TraceID == r.TraceID {
					found = true
				}
			}
			if !found {
				add(sessionvo.OperationCallFact{OperationID: r.OperationID, Attempt: r.Attempt, ToolName: r.ToolName}, "record_content_missing", "evidence")
			}
		}
	}
	for _, op := range snapshot.Operations {
		if op.InteractionID != interaction.ID || op.ConversationID != interaction.ConversationID {
			return nil, errors.New("operation integrity identity mismatch")
		}
		last := op.Attempt
		if op.AttemptStatus == sessionvo.AttemptReady && last > 0 {
			last--
		}
		if last > 10000 {
			return nil, errors.New("integrity attempt budget exceeded")
		}
		for attempt := uint32(1); attempt <= last; attempt++ {
			if _, found := facts[key{op.ID, attempt}]; !found {
				add(sessionvo.OperationCallFact{OperationID: op.ID, Attempt: attempt, ToolName: op.ToolName}, "record_content_missing", "call_record")
			}
		}
	}
	if interaction.ClosureManifest != nil {
		for _, expected := range interaction.ClosureManifest.ExpectedReceipts {
			if !expected.Required {
				continue
			}
			found := false
			for _, r := range snapshot.Receipts {
				found = found || r.ID == expected.ReceiptID
			}
			if !found {
				add(sessionvo.OperationCallFact{}, "record_content_missing", "receipt:"+expected.ReceiptID)
			}
		}
		for _, expected := range interaction.ClosureManifest.ExpectedOperations {
			if !expected.Required {
				continue
			}
			found := false
			for _, op := range snapshot.Operations {
				found = found || op.ID == expected.OperationID
			}
			if !found {
				add(sessionvo.OperationCallFact{OperationID: expected.OperationID}, "record_content_missing", "operation")
			}
		}
	}
	if len(report.Missing) > 0 {
		if !explicitGap && interaction.ClosureManifest != nil && interaction.ClosureManifest.AssemblerDeadline != nil && now.Before(*interaction.ClosureManifest.AssemblerDeadline) {
			return nil, nil
		}
		report.Status = "missing"
	}
	return report, nil
}

func payloadOmitted(payload *sessionvo.PayloadEnvelope) bool {
	return payload == nil || payload.Mode == sessionvo.PayloadOmitted
}

func integrityEventType(contract string) string {
	switch contract {
	case "ontology_schema_snapshot/v1":
		return "ontology.schema.snapshot"
	case "ontology_metric_result/v1":
		return "metric.query.completed"
	case "ontology_result/v1", "ontology_retrieval/v1", "ontology_subgraph/v1":
		return "retrieval.completed"
	case "data_query/v1", "mapped_sql_result/v1", "semantic_query_descriptor/v1":
		return "data.query.observed"
	default:
		return ""
	}
}

func hasRecordIntegrityCalls(snapshot sessionvo.EvidenceSnapshot) bool {
	if len(snapshot.CallFacts) > 0 {
		return true
	}
	for _, operation := range snapshot.Operations {
		if operation.Attempt > 0 && (operation.AttemptStatus != sessionvo.AttemptReady || operation.Attempt > 1) {
			return true
		}
	}
	manifest := snapshot.Interaction.ClosureManifest
	if manifest != nil {
		for _, reason := range manifest.SystemPartialReasons {
			if _, _, ok := sessionvo.ParseTraceCallGap(reason); ok {
				return true
			}
		}
	}
	return manifest != nil && (len(manifest.ExpectedOperations) > 0 || len(manifest.ExpectedReceipts) > 0 || slices.Contains(manifest.SystemPartialReasons, "not_collected_due_to_license") || slices.Contains(manifest.SystemPartialReasons, "not_collected_due_to_policy"))
}
func (s *Service) inspectRecordIntegrityWithScope(ctx context.Context, id string, scope evidencevo.QueryScope) (*evidencevo.RecordIntegrity, bool, error) {
	if !s.currentRecordIntegrity {
		return nil, false, nil
	}
	interaction, _, authorized, err := s.authorizeRecordIntegrityInteraction(ctx, id, scope)
	if err != nil || !authorized {
		return nil, false, err
	}
	return storedRecordIntegrity(interaction)
}

func (s *Service) authorizeRecordIntegrityInteraction(ctx context.Context, id string, scope evidencevo.QueryScope) (sessionvo.Interaction, sessionvo.Owner, bool, error) {
	var interaction sessionvo.Interaction
	var owner sessionvo.Owner
	authorized := false
	if s.sessionStore == nil {
		return interaction, owner, false, errors.New("record integrity session source unavailable")
	}
	err := s.sessionStore.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		i, found := tx.PeekInteraction(id)
		if !found {
			return nil
		}
		c, found := tx.PeekConversation(i.ConversationID)
		if !found || !canReadCanonicalConversation(c, scope) {
			return nil
		}
		interaction, owner, authorized = i, c.Owner, true
		return nil
	})
	return interaction, owner, authorized, err
}

// Live inspection is an internal generation path. Page reads consume the
// persisted verdict; they never fetch call payloads or external evidence.
func (s *Service) inspectLiveRecordIntegrity(ctx context.Context, id string, scope evidencevo.QueryScope) (*evidencevo.RecordIntegrity, error) {
	report, _, _, err := s.inspectLiveRecordIntegrityWithScope(ctx, id, scope)
	return report, err
}

func (s *Service) inspectLiveRecordIntegrityWithScope(ctx context.Context, id string, scope evidencevo.QueryScope) (*evidencevo.RecordIntegrity, bool, uint64, error) {
	if !s.currentRecordIntegrity {
		return nil, false, 0, nil
	}
	interaction, owner, authorized, err := s.authorizeRecordIntegrityInteraction(ctx, id, scope)
	if err != nil || !authorized {
		return nil, false, 0, err
	}
	return s.inspectAuthorizedLiveRecordIntegrity(ctx, interaction.ID, interaction.ConversationID, owner, scope)
}

func (s *Service) inspectAuthorizedLiveRecordIntegrity(ctx context.Context, id, conversationID string, owner sessionvo.Owner, scope evidencevo.QueryScope) (*evidencevo.RecordIntegrity, bool, uint64, error) {
	reader, ok := s.sessionStore.(isessionstore.EvidenceSnapshotReader)
	if !ok {
		return nil, false, 0, errors.New("record integrity snapshot source unavailable")
	}
	snapshot, found, err := reader.ReadEvidenceSnapshot(ctx, id)
	if err != nil {
		return nil, false, 0, err
	}
	if !found {
		return nil, false, 0, errors.New("registered interaction disappeared during integrity check")
	}
	if snapshot.Interaction.ID != id || snapshot.Interaction.ConversationID != conversationID {
		return nil, false, 0, errors.New("record integrity snapshot identity mismatch")
	}
	if snapshot.Interaction.ExecutionStatus == sessionvo.InteractionActive || !hasRecordIntegrityCalls(snapshot) {
		return nil, false, snapshot.Interaction.IntegritySourceVersion, nil
	}
	// Artifact misses through a caller-filtered store are not authoritative
	// absence: do not turn authorization or unsupported external reads into gaps.
	ctx = withRecordIntegrityReadBudget(ctx)
	budget := ctx.Value(recordIntegrityBudgetKey{}).(*recordIntegrityReadBudget)
	// Reuse verified metadata only within this authorized interaction check.
	// Output/receipt checks need presence, not the potentially large JSON body.
	type verifiedArtifact struct {
		source                             evidencevo.EvidenceArtifact
		canonicalLength, htmlEscapedLength int
	}
	verified := map[string]verifiedArtifact{}
	operations := captureOperationIDs(snapshot)
	checkSource := func(id string, artifact evidencevo.EvidenceArtifact, f sessionvo.OperationCallFact, field string) error {
		if artifact.ArtifactID != id {
			return errors.New("call payload artifact identity mismatch")
		}
		if field == "artifact" {
			if reason := captureSourceMismatch(id, snapshot.Interaction.ID, artifact, []ArtifactCaptureSource{{RequestID: f.RequestID}}, operations); reason != "" {
				return fmt.Errorf("evidence artifact source mismatch: %s", reason)
			}
		} else if artifact.InteractionID != snapshot.Interaction.ID || artifact.OperationID != f.OperationID || artifact.RequestID != f.RequestID || artifact.TraceID != f.TraceID {
			return errors.New("call payload artifact source mismatch")
		}
		expectedType := map[string]evidencevo.ArtifactType{"input": evidencevo.ArtifactTypeQuery, "output": evidencevo.ArtifactTypeDataResult, "error": evidencevo.ArtifactTypeLogicExecution}[field]
		if expectedType != "" && artifact.ArtifactType != expectedType {
			return errors.New("call payload artifact type mismatch")
		}
		return nil
	}
	checkLength := func(p sessionvo.PayloadEnvelope, metadata verifiedArtifact) error {
		if p.ByteLength > 0 && p.ByteLength != metadata.canonicalLength && p.ByteLength != metadata.htmlEscapedLength {
			return errors.New("call payload artifact length mismatch")
		}
		return nil
	}
	verify := func(f sessionvo.OperationCallFact, field string, p sessionvo.PayloadEnvelope) (json.RawMessage, error) {
		id, valid := evidencevo.ArtifactIDFromReference(p.Ref)
		if !valid {
			return nil, nil
		}
		if metadata, found := verified[id]; found && (field == "output" || field == "artifact") {
			if err := checkSource(id, metadata.source, f, field); err != nil {
				return nil, err
			}
			if err := checkLength(p, metadata); err != nil {
				return nil, err
			}
			// These fields only consume non-nil presence in evaluateRecordIntegrity.
			return json.RawMessage("null"), nil
		}
		reader, ok := s.artifactStore.(iartifactstore.CaptureReader)
		if !ok {
			return nil, errors.New("authoritative bounded artifact source unavailable")
		}
		if budget.reads >= 128 || budget.bytes >= 32<<20 {
			return nil, errors.New("integrity artifact read budget exceeded")
		}
		budget.reads++
		result, err := reader.ReadArtifactForCapture(ctx, id, scope, min(8<<20, (32<<20)-budget.bytes))
		budget.bytes += result.ReadBytes
		if err != nil {
			return nil, err
		}
		if !result.Found {
			if result.Exists {
				return nil, errors.New("referenced artifact inaccessible; integrity check incomplete")
			}
			return nil, nil
		}
		artifact := result.Artifact
		if err := checkSource(id, artifact, f, field); err != nil {
			return nil, err
		}
		if artifact.Content == nil {
			if artifact.SnapshotRef == "" {
				return nil, nil
			}
			return nil, errors.New("external artifact content is not available to integrity verifier")
		}
		content := evidencevo.CaptureArtifactContent(artifact, 8<<20)
		if content.State != evidencevo.ArtifactContentCaptured {
			return nil, fmt.Errorf("call artifact content verification failed: %s", content.Reason)
		}
		metadata := verifiedArtifact{
			source: evidencevo.EvidenceArtifact{
				ArtifactID: artifact.ArtifactID, ArtifactType: artifact.ArtifactType,
				InteractionID: artifact.InteractionID, OperationID: artifact.OperationID,
				RequestID: artifact.RequestID, TraceID: artifact.TraceID,
			},
			canonicalLength: len(content.CanonicalJSON), htmlEscapedLength: len(content.CanonicalJSON),
		}
		// The legacy producer used HTML escaping. In the hash-verified compact
		// canonical JSON each literal <, > or & becomes exactly six ASCII bytes;
		// all other bytes (including precise numbers and Unicode) stay unchanged.
		for _, b := range content.CanonicalJSON {
			if b == '<' || b == '>' || b == '&' {
				metadata.htmlEscapedLength += 5
			}
		}
		if err := checkLength(p, metadata); err != nil {
			return nil, err
		}
		if len(verified) < 128 {
			verified[id] = metadata
		}
		return content.CanonicalJSON, nil
	}
	report, err := evaluateRecordIntegrity(snapshot, owner, time.Now(), verify)
	return report, true, snapshot.Interaction.IntegritySourceVersion, err
}

func (s *Service) applyInteractionRecordIntegrity(ctx context.Context, entries []evidencevo.InteractionListSummary, scope evidencevo.QueryScope) error {
	if !s.currentRecordIntegrity {
		return nil
	}
	for i := range entries {
		report, applicable, err := s.inspectRecordIntegrityWithScope(ctx, entries[i].InteractionID, scope)
		if err != nil || (applicable && report == nil) {
			entries[i].RecordIntegrityCheckFailed = true
			continue
		}
		entries[i].CurrentRecordIntegrity = report
	}
	return nil
}

func (s *Service) applyConversationRecordIntegrity(ctx context.Context, entries []evidencevo.ConversationSummary, scope evidencevo.QueryScope) error {
	if !s.currentRecordIntegrity || len(entries) == 0 {
		return nil
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.ConversationID)
	}
	byConversation := map[string][]sessionvo.Interaction{}
	if err := s.sessionStore.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		conversations := tx.ListConversationsByIDs(ids)
		authorizedIDs := make([]string, 0, len(conversations))
		for _, id := range ids {
			if c, found := conversations[id]; found && canReadCanonicalConversation(c, scope) {
				authorizedIDs = append(authorizedIDs, id)
			}
		}
		byConversation = tx.ListInteractionsByConversationIDs(authorizedIDs)
		return nil
	}); err != nil {
		for i := range entries {
			entries[i].RecordIntegrityCheckFailed = true
		}
		return nil
	}
	for i := range entries {
		aggregate := &evidencevo.RecordIntegrity{Status: "complete", Scope: "registered_call_records", Missing: []evidencevo.MissingRecord{}}
		unfinished, checked := false, false
		for _, interaction := range byConversation[entries[i].ConversationID] {
			report, applicable, err := storedRecordIntegrity(interaction)
			if err != nil {
				entries[i].RecordIntegrityCheckFailed = true
				unfinished = true
				continue
			}
			if !applicable {
				continue
			}
			if report == nil {
				entries[i].RecordIntegrityCheckFailed = true
				unfinished = true
				continue
			}
			checked = true
			if aggregate.CheckedAt.IsZero() || report.CheckedAt.Before(aggregate.CheckedAt) {
				aggregate.CheckedAt = report.CheckedAt
			}
			aggregate.Missing = append(aggregate.Missing, report.Missing...)
			if report.Status == "missing" {
				aggregate.Status = "missing"
			}
		}
		if !checked || entries[i].RecordIntegrityCheckFailed || (unfinished && aggregate.Status == "complete") {
			continue
		}
		entries[i].CurrentRecordIntegrity = aggregate
	}
	return nil
}

// Only structured request IDs establish a known failed-call target. Invalid or
// absent parameters do not manufacture a business target requirement.
func integrityBusinessTargetRecorded(f sessionvo.OperationCallFact, r sessionvo.Receipt, events []ledgervo.Event, owner sessionvo.Owner, input, recordedError json.RawMessage) (bool, error) {
	// An already-confirmed missing input has its own reason; do not infer a
	// target from that missing content or erase the known gap with a read error.
	if len(input) == 0 {
		return true, nil
	}
	if f.Status == sessionvo.AttemptFailed {
		var failure struct {
			Stage      string `json:"stage"`
			ErrorStage string `json:"error_stage"`
		}
		if len(recordedError) == 0 {
			return true, nil
		}
		_ = json.Unmarshal(recordedError, &failure)
		if failure.Stage == "input_validation" || failure.ErrorStage == "input_validation" {
			return true, nil
		}
	}

	refs := map[string]bool{}
	for _, ref := range r.BusinessRefs {
		if ref.IsCanonical() || historicalFunctionRefShape(ref) {
			refs[ref.RefID] = true
		}
	}
	for _, e := range events {
		if !e.Owner.Equal(owner) || e.InteractionID != f.InteractionID || e.OperationID != f.OperationID || e.Attempt != f.Attempt || e.RequestID != f.RequestID || e.TraceID != f.TraceID {
			continue
		}
		for _, ref := range e.BusinessRefs {
			if ref.IsCanonical() || historicalFunctionRefShape(ref) {
				refs[ref.RefID] = true
			}
		}
		var envelope struct {
			Payload map[string]json.RawMessage `json:"payload"`
		}
		// Producer events use typed reference collections with ref_id; arbitrary
		// output text and caller declarations never establish business identity.
		if json.Unmarshal(e.Envelope, &envelope) != nil {
			continue
		}
		for _, field := range []string{"source_refs", "definition_refs", "resource_refs", "metric_refs"} {
			var observed []struct {
				RefID string `json:"ref_id"`
			}
			if json.Unmarshal(envelope.Payload[field], &observed) == nil {
				for _, ref := range observed {
					if ref.RefID != "" {
						refs[ref.RefID] = true
					}
				}
			}
		}
	}
	var args map[string]any
	_ = json.Unmarshal(input, &args)
	kn, _ := args["kn_id"].(string)
	toolbox, _ := args["toolbox_id"].(string)
	ot, _ := args["ot_id"].(string)
	metric, _ := args["metric_id"].(string)
	// Header-resolved network may not be repeated in the raw input. A known
	// object/metric still requires its canonical target; absence of kn_id is not
	// evidence that the request was rejected.
	for _, target := range []struct{ tool, prefix, id string }{{"query_object_instance", "object", ot}, {"query_metric", "metric", metric}} {
		if f.ToolName != target.tool || target.id == "" {
			continue
		}
		if kn != "" {
			return refs[target.prefix+":"+kn+":"+target.id], nil
		}
		for ref := range refs {
			parts := strings.Split(ref, ":")
			if len(parts) == 3 && parts[0] == target.prefix && parts[1] != "" && parts[2] == target.id {
				return true, nil
			}
		}
		return false, nil
	}
	if f.ToolName == "explore_subgraph" {
		id, _ := args["source_object_type_id"].(string)
		if id != "" {
			for ref := range refs {
				parts := strings.Split(ref, ":")
				if len(parts) == 3 && parts[0] == "object" && parts[2] == id && (kn == "" || parts[1] == kn) {
					return true, nil
				}
			}
			return false, nil
		}
	}
	// The SQL contract records physical resources; an unrelated network/object
	// reference cannot stand in for a resource observation.
	if f.CapabilityProfile != nil && f.CapabilityProfile.EvidenceContract == "mapped_sql_result/v1" {
		for ref := range refs {
			if strings.HasPrefix(ref, "resource:") && len(ref) > len("resource:") {
				return true, nil
			}
		}
		if f.Status == sessionvo.AttemptCompleted {
			return false, nil
		}
	}
	if f.CapabilityProfile != nil && f.CapabilityProfile.EvidenceContract == "managed_function_execution/v1" {
		for ref := range refs {
			if matchesFunctionBusinessRef(ref, kn, toolbox, f.ToolName) {
				return true, nil
			}
		}
		return false, nil
	}
	var target string
	if kn != "" {
		switch f.ToolName {
		case "get_kn_detail", "get_object_types", "get_relation_types", "run_cypher":
			target = "kn:" + kn
		}
	}

	if target != "" {
		return refs[target], nil
	}
	if f.Status == sessionvo.AttemptFailed && len(refs) == 0 {
		return false, errors.New("failed-call target context unavailable; no explicit input rejection")
	}

	return len(refs) > 0, nil
}

// matchesFunctionBusinessRef accepts the current three-segment contract and
// the historical four-segment contract written by older managed-function
// producers. The compatibility is read-only: new producers still emit the
// canonical function:<kn_id>:<tool_id> form.
func matchesFunctionBusinessRef(ref, kn, toolbox, tool string) bool {
	parts := strings.Split(ref, ":")
	if len(parts) == 3 && parts[0] == "function" && parts[1] != "" && parts[2] == tool {
		return kn == "" || parts[1] == kn
	}
	return len(parts) == 4 && parts[0] == "function" && parts[1] != "" && parts[2] != "" && parts[3] == tool && kn != "" && toolbox != "" && parts[1] == kn && parts[2] == toolbox
}

func historicalFunctionRefShape(ref sessionvo.BusinessRef) bool {
	if ref.RefType != sessionvo.BusinessRefFunction || ref.Version == "" {
		return false
	}
	parts := strings.Split(ref.RefID, ":")
	return len(parts) == 4 && parts[0] == "function" && parts[1] != "" && parts[2] != "" && parts[3] != ""
}

// Projection loss must not remove a registered conversation from integrity
// checks. Identity inventory is bounded and retains the store's owner/date/
// excluded-Agent predicates; missing projections never establish KN access.
func (s *Service) appendRegisteredConversationCandidates(ctx context.Context, entries []evidencevo.ConversationSummary, grouped map[string][]evidencevo.RequestSummary, options evidencevo.SummaryQueryOptions, metadata *summaryLoadMetadata) ([]evidencevo.ConversationSummary, error) {
	if !s.currentRecordIntegrity || !trustedQueryScope(options.Scope) {
		return entries, nil
	}
	// These filters need surviving call/KN projection facts. An empty summary
	// cannot prove a match and must not be added to their result set.
	if options.Tool != "" || options.Service != "" || options.ErrorKeyword != "" || options.KnowledgeNetwork != "" || options.EvidenceCompleteness != "" || options.Keyword != "" || options.AgentOrApp != "" || options.TraceID != "" || options.InteractionID != "" {
		return entries, nil
	}
	pageStore, ok := s.sessionStore.(isessionstore.SummaryPageStore)
	if !ok {
		return entries, nil
	}
	existing := make(map[string]bool, len(entries))
	for _, entry := range entries {
		existing[entry.ConversationID] = true
	}
	query := summaryIdentityQuery(options)
	query.IncludeRegisteredCalls = true
	query.Offset, query.AfterStartedAt, query.AfterID = 0, "", ""
	query.Limit = MaxSummaryQueryLimit
	identities := make([]string, 0)
	for len(identities) < MaxSummaryScanEntries {
		page, err := pageStore.ListConversationSummaryIdentities(ctx, query)
		if err != nil {
			return entries, err
		}
		for _, identity := range page.Entries {
			if !existing[identity.ID] && (options.ConversationID == "" || identity.ID == options.ConversationID) {
				identities = append(identities, identity.ID)
				existing[identity.ID] = true
			}
		}
		if !page.HasMore || len(page.Entries) == 0 {
			break
		}
		query.Offset += len(page.Entries)
		if query.Offset >= MaxSummaryScanEntries {
			metadata.addReason("conversation_identity_scan_cap_reached")
			break
		}
	}
	err := s.sessionStore.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		conversations := tx.ListConversationsByIDs(identities)
		for _, id := range identities {
			conversation, found := conversations[id]
			if !found || !canReadCanonicalConversation(conversation, options.Scope) {
				continue
			}
			interactions := tx.ListInteractions(id)
			registered := false
			for _, interaction := range interactions {
				if len(tx.ListOperationCallFacts(interaction.ID)) > 0 {
					registered = true
					break
				}
				for _, operation := range tx.ListOperations(interaction.ID) {
					if operation.Attempt > 0 && (operation.AttemptStatus != sessionvo.AttemptReady || operation.Attempt > 1) {
						registered = true
						break
					}
				}
			}
			if !registered {
				continue
			}
			entries = append(entries, evidencevo.ConversationSummary{ConversationID: id, InteractionCount: len(interactions)})
			grouped[id] = canonicalConversationRequestGroups(map[string][]sessionvo.Interaction{id: interactions})[id]
		}
		return nil
	})
	return entries, err
}
