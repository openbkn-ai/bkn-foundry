package assemblysvc

import (
	"fmt"
	"sort"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
)

type ClaimAssembly struct {
	Claim            sessionvo.Claim          `json:"claim"`
	Completeness     sessionvo.EvidenceStatus `json:"completeness"`
	AdoptedSupports  []sessionvo.ClaimSupport `json:"adopted_supports"`
	RejectedSupports []sessionvo.ClaimSupport `json:"rejected_supports"`
	// UnusedEvidenceRefs are refs neither adopted nor rejected by this Claim.
	UnusedEvidenceRefs []sessionvo.EvidenceRef `json:"unused_evidence_refs"`
	PartialReasons     []string                `json:"partial_reasons"`
}

type Result struct {
	Completeness           sessionvo.EvidenceStatus          `json:"completeness"`
	Claims                 []ClaimAssembly                   `json:"claims"`
	Events                 []EventNode                       `json:"events"`
	BusinessRefs           []sessionvo.BusinessRef           `json:"business_refs"`
	ArtifactRefs           []string                          `json:"artifact_refs"`
	EvidenceRefs           []sessionvo.EvidenceRef           `json:"evidence_refs"`
	OperationBusinessEdges []sessionvo.OperationBusinessEdge `json:"operation_business_edges"`
	// UnusedEvidenceRefs are refs neither adopted nor rejected by any Claim in this revision.
	UnusedEvidenceRefs []sessionvo.EvidenceRef `json:"unused_evidence_refs"`
	IncludedEventIDs   []string                `json:"included_event_ids"`
	EventLayers        map[string]int          `json:"event_layers"`
	PartialReasons     []string                `json:"partial_reasons"`
}

type EventNode struct {
	EventID           string    `json:"event_id"`
	EventType         string    `json:"event_type"`
	OperationID       string    `json:"operation_id,omitempty"`
	CausationEventIDs []string  `json:"causation_event_ids,omitempty"`
	ProducerStreamID  string    `json:"producer_stream_id"`
	ProducerEpoch     uint64    `json:"producer_epoch"`
	ProducerSequence  uint64    `json:"producer_sequence"`
	StartedAt         time.Time `json:"started_at"`
	ObservedAt        time.Time `json:"observed_at"`
	EmittedAt         time.Time `json:"emitted_at"`
	Layer             int       `json:"layer"`
}

func Assemble(interactionID string, events []ledgervo.Event, expectedClaimIDs []string) Result {
	return assemble(interactionID, events, expectedClaimIDs, nil)
}

func AssembleWithExternalEvidence(
	interactionID string,
	events []ledgervo.Event,
	expectedClaimIDs []string,
	externalEvidence []sessionvo.EvidenceRef,
) Result {
	return assemble(interactionID, events, expectedClaimIDs, externalEvidence)
}

func assemble(
	interactionID string,
	events []ledgervo.Event,
	expectedClaimIDs []string,
	externalEvidence []sessionvo.EvidenceRef,
) Result {
	result := Result{
		Claims:                 []ClaimAssembly{},
		Events:                 []EventNode{},
		BusinessRefs:           []sessionvo.BusinessRef{},
		ArtifactRefs:           []string{},
		EvidenceRefs:           []sessionvo.EvidenceRef{},
		OperationBusinessEdges: []sessionvo.OperationBusinessEdge{},
		UnusedEvidenceRefs:     []sessionvo.EvidenceRef{},
		IncludedEventIDs:       []string{},
		EventLayers:            make(map[string]int),
		PartialReasons:         []string{},
	}
	eventsByID := make(map[string]ledgervo.Event, len(events))
	claimsByID := make(map[string]sessionvo.Claim)
	evidenceByRef := make(map[string]sessionvo.EvidenceRef, len(externalEvidence))
	artifactRefs := make(map[string]struct{})
	for _, ref := range externalEvidence {
		evidenceByRef[evidenceRefKey(ref)] = ref
	}
	businessByKey := make(map[string]sessionvo.BusinessRef)
	for _, event := range events {
		if event.InteractionID != interactionID {
			continue
		}
		eventsByID[event.EventID] = event
		result.IncludedEventIDs = append(result.IncludedEventIDs, event.EventID)
		for _, ref := range event.EvidenceRefs {
			evidenceByRef[evidenceRefKey(ref)] = ref
		}
		for _, ref := range event.ArtifactRefs {
			artifactRefs[ref] = struct{}{}
		}
		for _, ref := range event.BusinessRefs {
			businessByKey[businessRefKey(ref)] = ref
		}
		for _, edge := range event.OperationBusinessEdges {
			result.OperationBusinessEdges = append(result.OperationBusinessEdges, edge)
			businessByKey[businessRefKey(edge.BusinessRef)] = edge.BusinessRef
		}
		for _, claim := range event.Claims {
			claimsByID[claim.ID] = claim
		}
	}
	sort.Strings(result.IncludedEventIDs)
	result.EventLayers, result.PartialReasons = eventLayers(eventsByID)
	result.Events = eventNodes(eventsByID, result.EventLayers)
	result.BusinessRefs = sortedBusinessRefs(businessByKey)
	result.ArtifactRefs = sortedStringSet(artifactRefs)
	result.EvidenceRefs = sortedTypedEvidenceRefs(evidenceByRef)
	sort.Slice(result.OperationBusinessEdges, func(i, j int) bool {
		left, right := result.OperationBusinessEdges[i], result.OperationBusinessEdges[j]
		return left.OperationID+"\x00"+string(left.Role)+"\x00"+businessRefKey(left.BusinessRef) <
			right.OperationID+"\x00"+string(right.Role)+"\x00"+businessRefKey(right.BusinessRef)
	})

	if len(expectedClaimIDs) == 0 {
		result.Completeness = sessionvo.EvidenceNotApplicable
		if len(result.PartialReasons) > 0 {
			result.Completeness = sessionvo.EvidencePartial
		}
		result.UnusedEvidenceRefs = sortedEvidenceRefs(evidenceByRef, nil)
		return result
	}
	result.Completeness = sessionvo.EvidenceComplete
	globallyClassified := make(map[string]struct{})
	for _, claimID := range expectedClaimIDs {
		claim, found := claimsByID[claimID]
		if !found {
			result.Completeness = sessionvo.EvidencePartial
			result.PartialReasons = append(result.PartialReasons, "claim_missing:"+claimID)
			continue
		}
		assembled := assembleClaim(claim, evidenceByRef)
		result.Claims = append(result.Claims, assembled)
		for _, support := range append(append([]sessionvo.ClaimSupport(nil), assembled.AdoptedSupports...), assembled.RejectedSupports...) {
			if ref, found := supportMatchesEvidence(support, evidenceByRef); found {
				globallyClassified[evidenceRefKey(ref)] = struct{}{}
			}
		}
		if claim.Status == sessionvo.ClaimAsserted && claim.Materiality == sessionvo.ClaimMaterial &&
			assembled.Completeness != sessionvo.EvidenceComplete {
			result.Completeness = sessionvo.EvidencePartial
			result.PartialReasons = append(result.PartialReasons, assembled.PartialReasons...)
		}
	}
	if len(result.PartialReasons) > 0 {
		result.Completeness = sessionvo.EvidencePartial
	}
	result.UnusedEvidenceRefs = sortedEvidenceRefs(evidenceByRef, globallyClassified)
	return result
}

func sortedStringSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func eventNodes(events map[string]ledgervo.Event, layers map[string]int) []EventNode {
	result := make([]EventNode, 0, len(events))
	for _, event := range events {
		result = append(result, EventNode{
			EventID: event.EventID, EventType: event.EventType, OperationID: event.OperationID,
			CausationEventIDs: append([]string(nil), event.CausationEventIDs...),
			ProducerStreamID:  event.ProducerStreamID, ProducerEpoch: event.ProducerEpoch,
			ProducerSequence: event.ProducerSequence, StartedAt: event.StartedAt,
			ObservedAt: event.ObservedAt, EmittedAt: event.EmittedAt, Layer: layers[event.EventID],
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Layer != result[j].Layer {
			return result[i].Layer < result[j].Layer
		}
		return result[i].EventID < result[j].EventID
	})
	return result
}

func businessRefKey(ref sessionvo.BusinessRef) string {
	asOf := ""
	if ref.AsOf != nil {
		asOf = ref.AsOf.UTC().Format(time.RFC3339Nano)
	}
	return fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s", ref.RefType, ref.RefID, ref.BusinessDomainID, ref.Version, asOf)
}

func sortedBusinessRefs(values map[string]sessionvo.BusinessRef) []sessionvo.BusinessRef {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]sessionvo.BusinessRef, 0, len(keys))
	for _, key := range keys {
		result = append(result, values[key])
	}
	return result
}

func sortedTypedEvidenceRefs(values map[string]sessionvo.EvidenceRef) []sessionvo.EvidenceRef {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]sessionvo.EvidenceRef, 0, len(keys))
	for _, key := range keys {
		result = append(result, values[key])
	}
	return result
}

func evidenceRefKey(ref sessionvo.EvidenceRef) string {
	asOf := ""
	if ref.AsOf != nil {
		asOf = ref.AsOf.UTC().Format(time.RFC3339Nano)
	}
	return fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s",
		ref.Ref, ref.RefType, ref.SourceInteractionID, ref.SourceRevisionID,
		ref.SourceOperationID, ref.Version, ref.ContentHash, ref.FragmentSelector, asOf)
}

func assembleClaim(claim sessionvo.Claim, evidence map[string]sessionvo.EvidenceRef) ClaimAssembly {
	result := ClaimAssembly{
		Claim:              claim,
		Completeness:       sessionvo.EvidenceComplete,
		AdoptedSupports:    []sessionvo.ClaimSupport{},
		RejectedSupports:   []sessionvo.ClaimSupport{},
		UnusedEvidenceRefs: []sessionvo.EvidenceRef{},
		PartialReasons:     []string{},
	}
	adoptedRoles := make(map[string]struct{})
	classifiedRefs := make(map[string]struct{})
	for _, support := range claim.Supports {
		switch support.Status {
		case sessionvo.SupportAdopted:
			adoptedRoles[support.Role] = struct{}{}
			matched, status := matchSupportEvidence(support, evidence)
			if status != supportMatched {
				result.Completeness = sessionvo.EvidencePartial
				result.PartialReasons = append(result.PartialReasons, supportMatchReason(status, claim.ID, support.TargetRef))
				continue
			}
			result.AdoptedSupports = append(result.AdoptedSupports, support)
			classifiedRefs[evidenceRefKey(matched)] = struct{}{}
		case sessionvo.SupportRejected:
			matched, status := matchSupportEvidence(support, evidence)
			if status != supportMatched {
				result.Completeness = sessionvo.EvidencePartial
				result.PartialReasons = append(result.PartialReasons, supportMatchReason(status, claim.ID, support.TargetRef))
				continue
			}
			result.RejectedSupports = append(result.RejectedSupports, support)
			classifiedRefs[evidenceRefKey(matched)] = struct{}{}
		}
	}
	if claim.Status == sessionvo.ClaimAsserted {
		for _, role := range claim.RequiredSupportRoles {
			if _, found := adoptedRoles[role]; !found {
				result.Completeness = sessionvo.EvidencePartial
				result.PartialReasons = append(result.PartialReasons, "required_support_missing:"+claim.ID+":"+role)
			}
		}
	} else {
		result.Completeness = sessionvo.EvidenceNotApplicable
		result.PartialReasons = []string{}
	}
	result.UnusedEvidenceRefs = sortedEvidenceRefs(evidence, classifiedRefs)
	return result
}

type supportMatchStatus string

const (
	supportMatched    supportMatchStatus = "matched"
	supportUnresolved supportMatchStatus = "unresolved"
	supportAmbiguous  supportMatchStatus = "ambiguous"
)

func supportMatchReason(status supportMatchStatus, claimID, targetRef string) string {
	if status == supportAmbiguous {
		return "support_target_ambiguous:" + claimID + ":" + targetRef
	}
	return "support_target_unresolved:" + claimID + ":" + targetRef
}

func supportMatchesEvidence(
	support sessionvo.ClaimSupport,
	evidence map[string]sessionvo.EvidenceRef,
) (sessionvo.EvidenceRef, bool) {
	matched, status := matchSupportEvidence(support, evidence)
	return matched, status == supportMatched
}

func matchSupportEvidence(
	support sessionvo.ClaimSupport,
	evidence map[string]sessionvo.EvidenceRef,
) (sessionvo.EvidenceRef, supportMatchStatus) {
	matches := matchingEvidence(support, evidence)
	if len(matches) == 0 {
		return sessionvo.EvidenceRef{}, supportUnresolved
	}
	if len(matches) > 1 {
		return sessionvo.EvidenceRef{}, supportAmbiguous
	}
	return matches[0], supportMatched
}

func matchingEvidence(support sessionvo.ClaimSupport, evidence map[string]sessionvo.EvidenceRef) []sessionvo.EvidenceRef {
	matches := make(map[string]sessionvo.EvidenceRef)
	for key, ref := range evidence {
		if ref.Ref != support.TargetRef || ref.SourceInteractionID != support.SourceInteractionID ||
			ref.SourceRevisionID != support.SourceRevisionID || ref.Version != support.Version ||
			ref.ContentHash != support.ContentHash {
			continue
		}
		if support.SourceOperationID != "" && ref.SourceOperationID != support.SourceOperationID {
			continue
		}
		if support.FragmentSelector != "" && ref.FragmentSelector != support.FragmentSelector {
			continue
		}
		matchesType := false
		switch support.TargetType {
		case sessionvo.SupportArtifactFragment:
			matchesType = ref.RefType == sessionvo.EvidenceRefArtifactFragment
		case sessionvo.SupportOperationOutput:
			matchesType = ref.RefType == sessionvo.EvidenceRefOperationOutput
		case sessionvo.SupportClaim:
			matchesType = ref.RefType == sessionvo.EvidenceRefClaim
		case sessionvo.SupportEvidence:
			matchesType = ref.RefType == sessionvo.EvidenceRefEvent || ref.RefType == sessionvo.EvidenceRefArtifact
		}
		if matchesType {
			matches[key] = ref
		}
	}
	return sortedTypedEvidenceRefs(matches)
}

func sortedEvidenceRefs(evidence map[string]sessionvo.EvidenceRef, used map[string]struct{}) []sessionvo.EvidenceRef {
	result := make(map[string]sessionvo.EvidenceRef)
	for key, ref := range evidence {
		if _, found := used[key]; !found {
			result[key] = ref
		}
	}
	return sortedTypedEvidenceRefs(result)
}

func eventLayers(events map[string]ledgervo.Event) (map[string]int, []string) {
	layers := make(map[string]int, len(events))
	visiting := make(map[string]bool, len(events))
	partialReasons := make([]string, 0)
	var visit func(string) int
	visit = func(eventID string) int {
		if layer, found := layers[eventID]; found {
			return layer
		}
		if visiting[eventID] {
			return 0
		}
		visiting[eventID] = true
		layer := 0
		for _, causeID := range events[eventID].CausationEventIDs {
			if _, found := events[causeID]; !found {
				partialReasons = append(partialReasons, "causation_missing:"+eventID+":"+causeID)
				continue
			}
			if candidate := visit(causeID) + 1; candidate > layer {
				layer = candidate
			}
		}
		visiting[eventID] = false
		layers[eventID] = layer
		return layer
	}
	for eventID := range events {
		visit(eventID)
	}
	sort.Strings(partialReasons)
	return layers, partialReasons
}
