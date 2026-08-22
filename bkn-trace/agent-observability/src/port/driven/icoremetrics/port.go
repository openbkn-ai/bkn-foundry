// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package icoremetrics

const (
	ConversationsTotal              = "bkn_trace_conversations_total"
	InteractionsTotal               = "bkn_trace_interactions_total"
	SessionRejectionsTotal          = "bkn_trace_session_rejections_total"
	InteractionsAbandonedTotal      = "bkn_trace_interactions_abandoned_total"
	SessionStoreErrorsTotal         = "bkn_trace_session_store_errors_total"
	SessionTransitionConflictsTotal = "bkn_trace_session_transition_conflicts_total"
	EvidenceIngestTotal             = "bkn_trace_evidence_ingest_total"
	EvidenceHashConflictsTotal      = "bkn_trace_evidence_hash_conflicts_total"
	ProjectionErrorsTotal           = "bkn_trace_projection_errors_total"
	ProjectionLagSeconds            = "bkn_trace_projection_lag_seconds"
	ProjectionReady                 = "bkn_trace_projection_ready"
	AssemblyLagSeconds              = "bkn_trace_assembly_lag_seconds"
)

type Recorder interface {
	Increment(name string)
	Add(name string, delta uint64)
	Set(name string, value float64)
}

type Noop struct{}

func (Noop) Increment(string)    {}
func (Noop) Add(string, uint64)  {}
func (Noop) Set(string, float64) {}
