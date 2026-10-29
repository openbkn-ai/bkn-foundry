// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// What a conflict between documents is about, which decides the document type that prevails.
const (
	conflictTerm     = "term"
	conflictRule     = "rule"
	conflictGoal     = "goal"
	conflictScope    = "scope"
	conflictScenario = "scenario"
)

// authority ranks document types for each kind of conflict. Terms and rules follow standards
// over policies over requirement documents; goals, scope and scenarios are the requirement
// document's to set, which standards and policies do not.
var authority = map[string]map[string]int{
	conflictTerm: {interfaces.REQUIREMENT_DOC_STANDARD: 3, interfaces.REQUIREMENT_DOC_POLICY: 2,
		interfaces.REQUIREMENT_DOC_PRD: 1},
	conflictRule: {interfaces.REQUIREMENT_DOC_STANDARD: 3, interfaces.REQUIREMENT_DOC_POLICY: 2,
		interfaces.REQUIREMENT_DOC_PRD: 1},
	conflictGoal:     {interfaces.REQUIREMENT_DOC_PRD: 1},
	conflictScope:    {interfaces.REQUIREMENT_DOC_PRD: 1},
	conflictScenario: {interfaces.REQUIREMENT_DOC_PRD: 1},
}

// suggestedDocument is the side of a conflict the order of authority prefers: the one document
// that outranks every other side. It is empty when the top ranks tie, a side's document is
// unknown, the kind has no order, or fewer than two documents disagree. It is a suggestion; the
// owner resolves the conflict.
func suggestedDocument(kind string, sides []interfaces.RequirementConflictSide,
	docs map[string]*interfaces.RequirementDocument) string {
	ranks, ok := authority[kind]
	if !ok {
		return ""
	}
	best, bestRank, tied := "", -1, false
	seen := map[string]bool{}
	for _, side := range sides {
		if seen[side.DocumentID] {
			continue
		}
		seen[side.DocumentID] = true
		d, ok := docs[side.DocumentID]
		if !ok {
			return ""
		}
		switch rank := ranks[d.DocType]; {
		case rank > bestRank:
			best, bestRank, tied = d.ID, rank, false
		case rank == bestRank:
			tied = true
		}
	}
	if len(seen) < 2 || tied {
		return ""
	}
	return best
}
