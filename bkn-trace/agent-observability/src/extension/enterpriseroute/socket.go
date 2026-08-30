// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

// Package enterpriseroute is the Trace Core assembly socket for optional
// enterprise HTTP routes. It deliberately knows nothing about licenses,
// capabilities, BKN mapping, or enterprise implementations.
package enterpriseroute

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionoutbox"
)

// InteractionFacts is the one Core-owned input EE receives for a selected
// interaction. It deliberately contains observed call facts, not any BKN
// mapping or business interpretation.
type InteractionFacts struct {
	Summary    evidencevo.InteractionSummary  `json:"summary"`
	Operations []sessionvo.OperationExecution `json:"operations"`
	// SourceRead carries the current caller context only for EE's subsequent
	// source-BKN verification. It must never be serialized into an EE response,
	// Markdown export, log field, or parser result.
	SourceRead SourceReadContext `json:"-"`
}

// ListQuery is the deliberately narrow filter surface used by EE's existing
// business-session list. Core owns the record scope; EE may not supply one.
type ListQuery struct {
	ConversationID string
	Page           int
	PageSize       int
	Keyword        string
	Status         string
	AgentOrApp     string
	// ExcludeAgentOrApp is supplied only by the assembled EE module. It is
	// intentionally absent from the public HTTP query surface.
	ExcludeAgentOrApp    string
	KnowledgeNetwork     string
	EvidenceCompleteness string
}

// SourceReadContext is the caller context forwarded to a source BKN read. It
// is intentionally an in-process value, not a wire DTO.
type SourceReadContext struct {
	Authorization          string `json:"-"`
	TenantID               string `json:"-"`
	AccountID              string `json:"-"`
	AccountType            string `json:"-"`
	ApplicationPrincipalID string `json:"-"`
	EffectiveSubjectType   string `json:"-"`
	EffectiveSubjectID     string `json:"-"`
	DelegationID           string `json:"-"`
}

// Reader reads one interaction only after Core has applied the caller's Trace
// record scope. Implementations must not expose a store or bypass that scope.
type Reader interface {
	ReadInteraction(context.Context, string) (InteractionFacts, bool, error)
	ListConversations(context.Context, ListQuery) (evidencevo.ConversationSummaryPage, error)
	ListInteractions(context.Context, ListQuery) (evidencevo.InteractionSummaryPage, error)
}

// Registrar is the only way an EE mounter may add a route. It fixes the
// security order: EE availability gate first, then Core record-scope
// authorization, then the EE handler.
type Registrar interface {
	Handle(pattern string, availability func(http.Handler) http.Handler, handler http.Handler)
}

// Mounter adds enterprise-only routes during process assembly. Community builds
// leave the socket empty; the EE overlay registers its mounter before Core
// creates the public route tree.
type Mounter func(Registrar, Reader)

// HistoricalProvenanceEventHandler is assembled only by the EE overlay. Core
// retains the outbox lease; the overlay never leases or scans the outbox.
type HistoricalProvenanceEventHandler interface {
	HandleHistoricalProvenance(context.Context, iprojectionoutbox.Item) error
}

var registry struct {
	sync.Mutex
	mounter    Mounter
	provenance HistoricalProvenanceEventHandler
	frozen     bool
}

func RegisterHistoricalProvenanceHandler(handler HistoricalProvenanceEventHandler) {
	if handler == nil {
		panic("enterpriseroute: RegisterHistoricalProvenanceHandler(nil)")
	}
	registry.Lock()
	defer registry.Unlock()
	if registry.frozen || registry.provenance != nil {
		panic("enterpriseroute: provenance handler already frozen or registered")
	}
	registry.provenance = handler
}

func HistoricalProvenanceHandler() HistoricalProvenanceEventHandler {
	registry.Lock()
	defer registry.Unlock()
	return registry.provenance
}

// Register installs the one enterprise route mounter for this Core service.
// It must be called before Mount. A second registration is a process assembly
// error rather than an ambiguous route override.
func Register(mounter Mounter) {
	if mounter == nil {
		panic("enterpriseroute: Register(nil)")
	}
	registry.Lock()
	defer registry.Unlock()
	if registry.frozen {
		panic("enterpriseroute: Register after Mount")
	}
	if registry.mounter != nil {
		panic("enterpriseroute: mounter already registered")
	}
	registry.mounter = mounter
}

// Mount attaches the registered enterprise routes, if this binary has any.
// The Community binary has no registration and therefore no enterprise route.
func Mount(mux *http.ServeMux, reader Reader, authorize func(http.Handler) http.Handler) {
	if mux == nil {
		panic("enterpriseroute: Mount(nil)")
	}
	registry.Lock()
	mounter := registry.mounter
	registry.frozen = true
	registry.Unlock()
	if mounter != nil {
		if authorize == nil {
			panic("enterpriseroute: Mount requires Core authorization wrapper")
		}
		mounter(routeRegistrar{mux: mux, authorize: authorize}, reader)
	}
}

type routeRegistrar struct {
	mux       *http.ServeMux
	authorize func(http.Handler) http.Handler
}

func (r routeRegistrar) Handle(pattern string, availability func(http.Handler) http.Handler, handler http.Handler) {
	if availability == nil || handler == nil {
		panic("enterpriseroute: route requires availability gate and handler")
	}
	r.mux.Handle(pattern, availability(r.authorize(handler)))
}

// ResetForTest clears the process-local assembly socket between tests. It
// panics outside a test binary so a running service can never lose routes.
func ResetForTest() {
	if !testing.Testing() {
		panic("enterpriseroute: ResetForTest is test-only")
	}
	registry.Lock()
	defer registry.Unlock()
	registry.mounter = nil
	registry.provenance = nil
	registry.frozen = false
}
