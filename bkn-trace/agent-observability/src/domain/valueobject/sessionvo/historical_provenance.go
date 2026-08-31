// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionvo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"sort"
)

const HistoricalProvenanceBuildRequestedEventType = "historical_provenance.build_requested"

// HistoricalProvenanceBuildRequest is the sealed terminal fact snapshot carried
// by the Core outbox to the EE projection handler. It deliberately contains no
// caller credentials or mutable session state.
type HistoricalProvenanceBuildRequest struct {
	InteractionID       string              `json:"interaction_id"`
	FactsHash           string              `json:"facts_hash"`
	KnowledgeNetworkIDs []string            `json:"knowledge_network_ids"`
	Facts               []OperationCallFact `json:"facts"`
	ProjectionReadGrant string              `json:"projection_read_grant"`
}

func NewHistoricalProvenanceBuildRequest(
	interactionID string,
	facts []OperationCallFact,
) (HistoricalProvenanceBuildRequest, error) {
	if interactionID == "" {
		return HistoricalProvenanceBuildRequest{}, errors.New("historical provenance interaction ID is required")
	}
	canonicalFacts := append([]OperationCallFact(nil), facts...)
	slices.SortFunc(canonicalFacts, func(left, right OperationCallFact) int {
		if result := left.StartedAt.Compare(right.StartedAt); result != 0 {
			return result
		}
		if left.OperationID != right.OperationID {
			if left.OperationID < right.OperationID {
				return -1
			}
			return 1
		}
		return int(left.Attempt) - int(right.Attempt)
	})
	canonical, err := json.Marshal(canonicalFacts)
	if err != nil {
		return HistoricalProvenanceBuildRequest{}, err
	}
	sum := sha256.Sum256(canonical)
	return HistoricalProvenanceBuildRequest{
		InteractionID:       interactionID,
		FactsHash:           hex.EncodeToString(sum[:]),
		KnowledgeNetworkIDs: explicitKnowledgeNetworkIDs(canonicalFacts),
		Facts:               canonicalFacts,
	}, nil
}

func explicitKnowledgeNetworkIDs(facts []OperationCallFact) []string {
	set := make(map[string]struct{})
	for _, fact := range facts {
		if fact.Input.Mode != PayloadInline {
			continue
		}
		var input struct {
			KNID               string `json:"kn_id"`
			KnowledgeNetworkID string `json:"knowledge_network_id"`
		}
		if json.Unmarshal(fact.Input.Inline, &input) != nil {
			continue
		}
		if input.KNID != "" {
			set[input.KNID] = struct{}{}
			continue
		}
		if input.KnowledgeNetworkID != "" {
			set[input.KnowledgeNetworkID] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for networkID := range set {
		result = append(result, networkID)
	}
	sort.Strings(result)
	return result
}
