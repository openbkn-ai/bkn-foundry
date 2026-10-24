// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"sort"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func (tx memoryTransaction) ListFirstTraceFailureMetadata(scopes []isessionstore.TraceMetadataScope) []sessionvo.OperationCallFact {
	requested := map[isessionstore.TraceMetadataScope]bool{}
	for _, scope := range scopes {
		if scope.TraceID != "" {
			requested[scope] = true
		}
	}
	type identity struct{ interaction, trace, conversation string }
	first := map[identity]sessionvo.OperationCallFact{}
	for _, fact := range tx.s.operationCalls {
		if !requested[isessionstore.TraceMetadataScope{TraceID: fact.TraceID, ConversationID: fact.ConversationID, InteractionID: fact.InteractionID}] || fact.Status != sessionvo.AttemptFailed {
			continue
		}
		key := identity{fact.InteractionID, fact.TraceID, fact.ConversationID}
		old, found := first[key]
		if found && !(fact.StartedAt.Before(old.StartedAt) || fact.StartedAt.Equal(old.StartedAt) &&
			(fact.OperationID < old.OperationID || fact.OperationID == old.OperationID && fact.Attempt < old.Attempt)) {
			continue
		}
		metadata := sessionvo.OperationCallFact{
			TraceID: fact.TraceID, ConversationID: fact.ConversationID, InteractionID: fact.InteractionID,
			OperationID: fact.OperationID, Attempt: fact.Attempt, StartedAt: fact.StartedAt, Status: fact.Status,
		}
		if fact.Error != nil {
			payload := *fact.Error
			payload.Inline = append([]byte(nil), fact.Error.Inline...)
			metadata.Error = &payload
		}
		first[key] = metadata
	}
	result := make([]sessionvo.OperationCallFact, 0, len(first))
	for _, fact := range first {
		result = append(result, fact)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].InteractionID != result[j].InteractionID {
			return result[i].InteractionID < result[j].InteractionID
		}
		if result[i].TraceID != result[j].TraceID {
			return result[i].TraceID < result[j].TraceID
		}
		return result[i].ConversationID < result[j].ConversationID
	})
	return result
}

func (tx memoryTransaction) ListFirstTraceSourceMetadata(scopes []isessionstore.TraceMetadataScope) []sessionvo.OperationCallFact {
	requested := map[isessionstore.TraceMetadataScope]bool{}
	for _, scope := range scopes {
		if scope.TraceID != "" {
			requested[scope] = true
		}
	}
	type identity struct{ trace, conversation, interaction string }
	first := map[identity]sessionvo.OperationCallFact{}
	for _, fact := range tx.s.operationCalls {
		if !requested[isessionstore.TraceMetadataScope{TraceID: fact.TraceID, ConversationID: fact.ConversationID, InteractionID: fact.InteractionID}] || fact.SourceModule == "" {
			continue
		}
		key := identity{fact.TraceID, fact.ConversationID, fact.InteractionID}
		old, found := first[key]
		if found && !(fact.StartedAt.Before(old.StartedAt) || fact.StartedAt.Equal(old.StartedAt) &&
			(fact.OperationID < old.OperationID || fact.OperationID == old.OperationID && fact.Attempt < old.Attempt)) {
			continue
		}
		first[key] = sessionvo.OperationCallFact{TraceID: fact.TraceID, ConversationID: fact.ConversationID, InteractionID: fact.InteractionID,
			OperationID: fact.OperationID, Attempt: fact.Attempt, StartedAt: fact.StartedAt, SourceModule: fact.SourceModule}
	}
	result := make([]sessionvo.OperationCallFact, 0, len(first))
	for _, fact := range first {
		result = append(result, fact)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].TraceID != result[j].TraceID {
			return result[i].TraceID < result[j].TraceID
		}
		if result[i].ConversationID != result[j].ConversationID {
			return result[i].ConversationID < result[j].ConversationID
		}
		return result[i].InteractionID < result[j].InteractionID
	})
	return result
}
