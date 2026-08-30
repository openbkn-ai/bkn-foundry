// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package assemblysvc_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/assemblysvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
)

func TestAssembleRequiresDurableAdoptedSupportsForEveryMaterialClaimRole(t *testing.T) {
	t.Parallel()

	evidence := semanticEvent("evt-data", "op-query", 1)
	evidence.EvidenceRefs = []sessionvo.EvidenceRef{evidenceRef("evidence:june")}
	claims := semanticEvent("evt-claims", "op-answer", 2)
	claims.CausationEventIDs = []string{"evt-data"}
	claims.Claims = []sessionvo.Claim{
		claim("claim-a", sessionvo.SupportAdopted, ""),
		claim("claim-b", sessionvo.SupportRejected, "different period"),
	}

	result := assemblysvc.Assemble("int-1", []ledgervo.Event{claims, evidence}, []string{"claim-a", "claim-b"})

	if result.Completeness != sessionvo.EvidencePartial {
		t.Fatalf("rejected required support must leave the revision partial, got %q", result.Completeness)
	}
	if result.Claims[0].Completeness != sessionvo.EvidenceComplete ||
		result.Claims[1].Completeness != sessionvo.EvidencePartial {
		t.Fatalf("claim completeness must be independent: %#v", result.Claims)
	}
	if len(result.Claims[0].AdoptedSupports) != 1 || len(result.Claims[1].RejectedSupports) != 1 {
		t.Fatalf("support decisions were not preserved on edges: %#v", result.Claims)
	}
	if result.Claims[0].RejectedSupports == nil || result.Claims[0].PartialReasons == nil ||
		result.Claims[1].AdoptedSupports == nil {
		t.Fatalf("empty claim collections must be arrays: %#v", result.Claims)
	}
}

func TestAssembleReturnsArraysWhenInteractionHasNoClaims(t *testing.T) {
	t.Parallel()

	result := assemblysvc.Assemble("int-1", nil, nil)

	if result.Claims == nil || result.Events == nil || result.BusinessRefs == nil ||
		result.ArtifactRefs == nil || result.EvidenceRefs == nil ||
		result.OperationBusinessEdges == nil || result.UnusedEvidenceRefs == nil ||
		result.IncludedEventIDs == nil || result.PartialReasons == nil {
		t.Fatalf("empty assembly collections must be arrays: %#v", result)
	}
}

func TestAssembleReportsObservedButUnusedEvidenceWithoutIncreasingCompleteness(t *testing.T) {
	t.Parallel()

	event := semanticEvent("evt-data", "op-query", 1)
	event.EvidenceRefs = []sessionvo.EvidenceRef{evidenceRef("evidence:june"), evidenceRef("evidence:july")}
	event.Claims = []sessionvo.Claim{claim("claim-a", sessionvo.SupportAdopted, "")}

	result := assemblysvc.Assemble("int-1", []ledgervo.Event{event}, []string{"claim-a"})

	if result.Completeness != sessionvo.EvidenceComplete {
		t.Fatalf("adopted required support should complete the claim: %#v", result)
	}
	if len(result.UnusedEvidenceRefs) != 1 || result.UnusedEvidenceRefs[0].Ref != "evidence:july" {
		t.Fatalf("observed but unadopted evidence must be computed as unused: %#v", result.UnusedEvidenceRefs)
	}
}

func TestAssembleDoesNotCollapseDifferentVersionsOfTheSameEvidenceRef(t *testing.T) {
	t.Parallel()

	first := evidenceRef("evidence:forecast-total")
	second := first
	second.SourceRevisionID = "rev-source-2"
	second.Version = "2"
	second.ContentHash = "sha256:version-2"
	event := semanticEvent("evt-data", "op-query", 1)
	event.EvidenceRefs = []sessionvo.EvidenceRef{first, second}
	assertion := claim("claim-a", sessionvo.SupportAdopted, "")
	assertion.Supports[0].TargetRef = second.Ref
	assertion.Supports[0].SourceRevisionID = second.SourceRevisionID
	assertion.Supports[0].Version = second.Version
	assertion.Supports[0].ContentHash = second.ContentHash
	event.Claims = []sessionvo.Claim{assertion}

	result := assemblysvc.Assemble("int-1", []ledgervo.Event{event}, []string{"claim-a"})

	if result.Completeness != sessionvo.EvidenceComplete || len(result.EvidenceRefs) != 2 ||
		len(result.UnusedEvidenceRefs) != 1 || result.UnusedEvidenceRefs[0].Version != "1" {
		t.Fatalf("versioned evidence refs were collapsed or misclassified: %#v", result)
	}
}

func TestAssembleRejectsAdoptedSupportWhoseTargetIsMissingOrDrifted(t *testing.T) {
	t.Parallel()

	tests := map[string]func(*sessionvo.ClaimSupport){
		"missing target": func(support *sessionvo.ClaimSupport) { support.TargetRef = "evidence:missing" },
		"content drift":  func(support *sessionvo.ClaimSupport) { support.ContentHash = "sha256:changed" },
		"revision drift": func(support *sessionvo.ClaimSupport) { support.SourceRevisionID = "rev-other" },
	}
	for name, mutate := range tests {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			event := semanticEvent("evt-data", "op-query", 1)
			event.EvidenceRefs = []sessionvo.EvidenceRef{evidenceRef("evidence:june")}
			assertion := claim("claim-a", sessionvo.SupportAdopted, "")
			mutate(&assertion.Supports[0])
			event.Claims = []sessionvo.Claim{assertion}

			result := assemblysvc.Assemble("int-1", []ledgervo.Event{event}, []string{"claim-a"})

			if result.Completeness != sessionvo.EvidencePartial || result.Claims[0].Completeness != sessionvo.EvidencePartial {
				t.Fatalf("unresolved adopted support must be partial: %#v", result)
			}
		})
	}
}

func TestAssembleRejectsImpreciseRejectedSupport(t *testing.T) {
	t.Parallel()

	event := semanticEvent("evt-data", "op-query", 1)
	event.EvidenceRefs = []sessionvo.EvidenceRef{evidenceRef("evidence:june")}
	assertion := claim("claim-a", sessionvo.SupportRejected, "outside requested period")
	assertion.Supports[0].TargetRef = "evidence:not-observed"
	event.Claims = []sessionvo.Claim{assertion}

	result := assemblysvc.Assemble("int-1", []ledgervo.Event{event}, []string{"claim-a"})

	if result.Completeness != sessionvo.EvidencePartial || len(result.Claims[0].RejectedSupports) != 0 ||
		len(result.Claims[0].PartialReasons) == 0 {
		t.Fatalf("imprecise rejected support must be isolated as partial: %#v", result)
	}
}

func TestAssembleClassifiesAdoptedRejectedAndUnusedEvidenceAsDisjoint(t *testing.T) {
	t.Parallel()

	adopted := evidenceRef("evidence:adopted")
	rejected := evidenceRef("evidence:rejected")
	unused := evidenceRef("evidence:unused")
	assertion := claim("claim-a", sessionvo.SupportAdopted, "")
	assertion.Supports[0].TargetRef = adopted.Ref
	assertion.Supports[0].ContentHash = adopted.ContentHash
	rejectedSupport := assertion.Supports[0]
	rejectedSupport.TargetRef = rejected.Ref
	rejectedSupport.ContentHash = rejected.ContentHash
	rejectedSupport.Status = sessionvo.SupportRejected
	rejectedSupport.Reason = "not applicable"
	assertion.Supports = append(assertion.Supports, rejectedSupport)
	event := semanticEvent("evt-data", "op-query", 1)
	event.EvidenceRefs = []sessionvo.EvidenceRef{adopted, rejected, unused}
	event.Claims = []sessionvo.Claim{assertion}

	result := assemblysvc.Assemble("int-1", []ledgervo.Event{event}, []string{assertion.ID})

	if len(result.UnusedEvidenceRefs) != 1 || result.UnusedEvidenceRefs[0].Ref != unused.Ref {
		t.Fatalf("global evidence classes overlap: %#v", result)
	}
	if len(result.Claims[0].UnusedEvidenceRefs) != 1 || result.Claims[0].UnusedEvidenceRefs[0].Ref != unused.Ref {
		t.Fatalf("claim evidence classes overlap: %#v", result.Claims[0])
	}
}

func TestAssembleTreatsAmbiguousSupportTargetAsPartialDeterministically(t *testing.T) {
	t.Parallel()

	first := evidenceRef("evidence:ambiguous")
	second := first
	firstTime := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	secondTime := firstTime.Add(time.Hour)
	first.AsOf = &firstTime
	second.AsOf = &secondTime
	assertion := claim("claim-a", sessionvo.SupportAdopted, "")
	assertion.Supports[0].TargetRef = first.Ref
	assertion.Supports[0].ContentHash = first.ContentHash
	event := semanticEvent("evt-data", "op-query", 1)
	event.EvidenceRefs = []sessionvo.EvidenceRef{second, first}
	event.Claims = []sessionvo.Claim{assertion}

	for index := 0; index < 20; index++ {
		result := assemblysvc.Assemble("int-1", []ledgervo.Event{event}, []string{assertion.ID})
		if result.Completeness != sessionvo.EvidencePartial || len(result.Claims[0].PartialReasons) != 1 ||
			result.Claims[0].PartialReasons[0] != "support_target_ambiguous:claim-a:evidence:ambiguous" ||
			len(result.UnusedEvidenceRefs) != 2 {
			t.Fatalf("ambiguous support was selected nondeterministically: %#v", result)
		}
	}
}

func TestAssembleWithdrawnMaterialClaimDoesNotDegradeCurrentCompleteness(t *testing.T) {
	t.Parallel()

	assertion := claim("claim-withdrawn", sessionvo.SupportAdopted, "")
	assertion.Status = sessionvo.ClaimWithdrawn
	event := semanticEvent("evt-claim", "op-answer", 1)
	event.Claims = []sessionvo.Claim{assertion}

	result := assemblysvc.Assemble("int-1", []ledgervo.Event{event}, []string{assertion.ID})

	if result.Completeness != sessionvo.EvidenceComplete || len(result.Claims) != 1 ||
		result.Claims[0].Completeness != sessionvo.EvidenceNotApplicable || len(result.Claims[0].PartialReasons) != 0 {
		t.Fatalf("withdrawn claim degraded the active revision: %#v", result)
	}
}

func TestAssembleTopologicallyLayersParallelOperationsWithoutInventingOrder(t *testing.T) {
	t.Parallel()

	first := semanticEvent("evt-first", "op-june", 1)
	second := semanticEvent("evt-second", "op-july", 2)
	second.ObservedAt = first.ObservedAt.Add(-time.Minute)
	claimEvent := semanticEvent("evt-compare", "op-compare", 3)
	claimEvent.CausationEventIDs = []string{"evt-first", "evt-second"}

	result := assemblysvc.Assemble("int-1", []ledgervo.Event{claimEvent, second, first}, nil)

	if result.EventLayers["evt-first"] != 0 || result.EventLayers["evt-second"] != 0 ||
		result.EventLayers["evt-compare"] != 1 {
		t.Fatalf("parallel roots must remain peers regardless of timestamps: %#v", result.EventLayers)
	}
}

func TestAssembleMarksMissingCausationPartialEvenWithoutClaims(t *testing.T) {
	t.Parallel()

	event := semanticEvent("evt-child", "op-child", 1)
	event.CausationEventIDs = []string{"evt-parent-not-arrived"}

	result := assemblysvc.Assemble("int-1", []ledgervo.Event{event}, nil)

	if result.Completeness != sessionvo.EvidencePartial || len(result.PartialReasons) != 1 ||
		result.PartialReasons[0] != "causation_missing:evt-child:evt-parent-not-arrived" {
		t.Fatalf("missing causation was not isolated as partial: %#v", result)
	}
}

func TestAssembleReturnsTypedBusinessDimensionsAndKeepsVersionsDistinct(t *testing.T) {
	t.Parallel()

	asOf := time.Date(2026, 6, 30, 23, 59, 59, 0, time.UTC)
	event := semanticEvent("evt-query", "op-query", 1)
	event.BusinessRefs = []sessionvo.BusinessRef{
		{RefType: sessionvo.BusinessRefObjectType, RefID: "object:supplychain:forecast", Version: "1", AsOf: &asOf},
		{RefType: sessionvo.BusinessRefObjectType, RefID: "object:supplychain:forecast", Version: "2", AsOf: &asOf},
		{RefType: sessionvo.BusinessRefProperty, RefID: "property:supplychain:forecast:qty", Version: "1"},
	}
	event.EvidenceRefs = []sessionvo.EvidenceRef{evidenceRef("evidence:june")}
	event.ArtifactRefs = []string{"artifact:query-result", "artifact:answer"}
	event.OperationBusinessEdges = []sessionvo.OperationBusinessEdge{{
		OperationID: "op-query", BusinessRef: event.BusinessRefs[0],
		Role: sessionvo.OperationRoleAggregate, ObservedAt: event.ObservedAt,
	}}

	result := assemblysvc.Assemble("int-1", []ledgervo.Event{event}, nil)

	if len(result.Events) != 1 || result.Events[0].ObservedAt != event.ObservedAt || result.Events[0].Layer != 0 {
		t.Fatalf("process event timing/layer missing: %#v", result.Events)
	}
	if len(result.BusinessRefs) != 3 || result.BusinessRefs[0].AsOf == nil {
		t.Fatalf("typed business refs or as_of missing, or versions collapsed: %#v", result.BusinessRefs)
	}
	if len(result.EvidenceRefs) != 1 || len(result.OperationBusinessEdges) != 1 ||
		result.OperationBusinessEdges[0].Role != sessionvo.OperationRoleAggregate {
		t.Fatalf("categorized evidence/operation roles missing: %#v", result)
	}
	if len(result.ArtifactRefs) != 2 || result.ArtifactRefs[0] != "artifact:answer" {
		t.Fatalf("artifact drill-down refs missing or unstable: %#v", result.ArtifactRefs)
	}
}

func TestAssembleRichInteractionKeepsAllBusinessDimensionsAndClaimSpecificSupports(t *testing.T) {
	t.Parallel()

	refTypes := []sessionvo.BusinessRefType{
		sessionvo.BusinessRefKnowledgeNetwork, sessionvo.BusinessRefObjectType,
		sessionvo.BusinessRefObjectInstance, sessionvo.BusinessRefProperty,
		sessionvo.BusinessRefRelationType, sessionvo.BusinessRefDataResource,
		sessionvo.BusinessRefMetric, sessionvo.BusinessRefLogic, sessionvo.BusinessRefFunction,
		sessionvo.BusinessRefActionType, sessionvo.BusinessRefActionInstance,
	}
	roles := []sessionvo.OperationBusinessRole{
		sessionvo.OperationRoleRead, sessionvo.OperationRoleFilter, sessionvo.OperationRoleGroup,
		sessionvo.OperationRoleAggregate, sessionvo.OperationRoleInput, sessionvo.OperationRoleOutput,
		sessionvo.OperationRoleModify, sessionvo.OperationRoleRecommend, sessionvo.OperationRoleExecute,
	}
	refIDs := []string{
		"kn:supplychain", "object:supplychain:forecast",
		"object_instance:supplychain:forecast:row-1", "property:supplychain:forecast:qty",
		"relation:supplychain:contains", "resource:forecast-resource",
		"metric:supplychain:forecast-total", "logic:supplychain:forecast:aggregate",
		"function:supplychain:calculate", "action_type:supplychain:approve",
		"action_instance:supplychain:approve:run-1",
	}
	query := semanticEvent("evt-query", "op-query", 1)
	for index, refType := range refTypes {
		ref := sessionvo.BusinessRef{
			RefType: refType, RefID: refIDs[index],
			Version: "2026.07",
		}
		query.BusinessRefs = append(query.BusinessRefs, ref)
		query.OperationBusinessEdges = append(query.OperationBusinessEdges, sessionvo.OperationBusinessEdge{
			OperationID: query.OperationID, BusinessRef: ref,
			Role: roles[index%len(roles)], ObservedAt: query.ObservedAt,
		})
	}
	june := evidenceRef("evidence:june-rows")
	july := evidenceRef("evidence:july-rows")
	query.EvidenceRefs = []sessionvo.EvidenceRef{june, july}
	query.ArtifactRefs = []string{"artifact:june-rows", "artifact:july-rows"}

	claims := semanticEvent("evt-claims", "op-compare", 2)
	claims.CausationEventIDs = []string{query.EventID}
	claimJune := claim("claim-june-total", sessionvo.SupportAdopted, "")
	claimJune.Supports[0].TargetRef = june.Ref
	claimJune.Supports[0].ContentHash = june.ContentHash
	claimJuly := claim("claim-july-change", sessionvo.SupportAdopted, "")
	claimJuly.Supports[0].TargetRef = july.Ref
	claimJuly.Supports[0].ContentHash = july.ContentHash
	claims.Claims = []sessionvo.Claim{claimJune, claimJuly}

	result := assemblysvc.Assemble("int-1", []ledgervo.Event{claims, query}, []string{claimJune.ID, claimJuly.ID})

	if result.Completeness != sessionvo.EvidenceComplete || len(result.BusinessRefs) != len(refTypes) ||
		len(result.OperationBusinessEdges) != len(refTypes) || len(result.Claims) != 2 {
		t.Fatalf("rich interaction dimensions were collapsed: %#v", result)
	}
	if result.Claims[0].AdoptedSupports[0].TargetRef == result.Claims[1].AdoptedSupports[0].TargetRef {
		t.Fatalf("independent claims were assigned the same adopted support: %#v", result.Claims)
	}
}

func TestAssembleDAGIsStableAcrossEveryArrivalOrder(t *testing.T) {
	t.Parallel()

	left := semanticEvent("evt-left", "op-left", 1)
	right := semanticEvent("evt-right", "op-right", 1)
	join := semanticEvent("evt-join", "op-join", 2)
	join.CausationEventIDs = []string{left.EventID, right.EventID}
	orders := [][]ledgervo.Event{
		{left, right, join}, {left, join, right}, {right, left, join},
		{right, join, left}, {join, left, right}, {join, right, left},
	}
	want := map[string]int{"evt-left": 0, "evt-right": 0, "evt-join": 1}
	for _, events := range orders {
		result := assemblysvc.Assemble("int-1", events, nil)
		if !reflect.DeepEqual(result.EventLayers, want) {
			t.Fatalf("arrival order changed causal DAG: events=%v layers=%#v", result.IncludedEventIDs, result.EventLayers)
		}
	}
}

func semanticEvent(eventID, operationID string, sequence uint64) ledgervo.Event {
	return ledgervo.Event{
		EventID: eventID, InteractionID: "int-1", OperationID: operationID,
		ProducerStreamID: operationID, ProducerEpoch: 1, ProducerSequence: sequence,
		ObservedAt: time.Date(2026, 7, 30, 10, 0, int(sequence), 0, time.UTC),
	}
}

func evidenceRef(ref string) sessionvo.EvidenceRef {
	return sessionvo.EvidenceRef{
		Ref: ref, RefType: sessionvo.EvidenceRefArtifactFragment,
		SourceInteractionID: "int-1", SourceRevisionID: "rev-source-1",
		SourceOperationID: "op-query", ArtifactRef: "artifact:query",
		FragmentSelector: "rows", Version: "1", ContentHash: "sha256:" + ref,
	}
}

func claim(id string, status sessionvo.SupportStatus, reason string) sessionvo.Claim {
	return sessionvo.Claim{
		ID: id, Type: "answer", Materiality: sessionvo.ClaimMaterial,
		Status: sessionvo.ClaimAsserted, ContentArtifactRef: "artifact:answer",
		RequiredSupportRoles: []string{"source_data"},
		Supports: []sessionvo.ClaimSupport{{
			TargetRef: "evidence:june", TargetType: sessionvo.SupportArtifactFragment,
			SourceInteractionID: "int-1", SourceRevisionID: "rev-source-1",
			SourceOperationID: "op-query", Version: "1", ContentHash: "sha256:evidence:june",
			FragmentSelector: "rows", Role: "source_data", Status: status, Reason: reason,
		}},
	}
}
