// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package release

import (
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// releaseQuestions places the scenario questions of the knowledge layer by where what they need is:
// in the branch's definitions, which the release publishes, in main's, which are live, and in the
// diff between them. A need is named as the branch has it, else as main has it, else by its id.
func releaseQuestions(sq *interfaces.ScenarioQuestions, branchFiles map[string][]byte,
	mainFiles map[string][]byte, diff interfaces.PathDiff) interfaces.ReleaseQuestions {
	q := interfaces.ReleaseQuestions{
		AnswerableNew: []interfaces.ReleaseQuestion{}, Affected: []interfaces.ReleaseQuestion{},
		Unanswerable: []interfaces.ReleaseQuestion{},
	}
	if sq == nil {
		return q
	}
	q.SpecVersion = sq.SpecVersion
	branch, main := itemsOf(branchFiles), itemsOf(mainFiles)
	touched := map[interfaces.ReleaseItem]bool{}
	for _, p := range append(append([]string{}, diff.Changed...), diff.Removed...) {
		if kindOf(p) != "" {
			touched[key(itemAt(p, mainFiles))] = true
		}
	}

	for _, question := range sq.Questions {
		if question.Layer != interfaces.REQUIREMENT_LAYER_KNOWLEDGE {
			continue
		}
		q.Total++
		if len(question.Needs) == 0 {
			q.Unjudged++
			continue
		}
		needs, missing := []interfaces.ReleaseItem{}, []interfaces.ReleaseItem{}
		live, affected := true, false
		for _, n := range question.Needs {
			k := interfaces.ReleaseItem{Kind: n.Kind, ID: n.ID}
			it, inBranch := branch[k]
			if !inBranch {
				it = k
				it.Name = k.ID
				if m, ok := main[k]; ok {
					it = m
				}
				missing = append(missing, it)
			}
			needs = append(needs, it)
			if _, ok := main[k]; !ok {
				live = false
			}
			affected = affected || touched[k]
		}
		entry := interfaces.ReleaseQuestion{ID: question.ID, Text: question.Text, Needs: needs}
		switch {
		case live && affected:
			q.Affected = append(q.Affected, entry)
		case !live && len(missing) == 0:
			q.AnswerableNew = append(q.AnswerableNew, entry)
		}
		if len(missing) > 0 {
			entry.Missing = missing
			q.Unanswerable = append(q.Unanswerable, entry)
		}
	}
	return q
}

// itemsOf names the object types and relation types of a definition set, by kind and id.
func itemsOf(files map[string][]byte) map[interfaces.ReleaseItem]interfaces.ReleaseItem {
	items := map[interfaces.ReleaseItem]interfaces.ReleaseItem{}
	for p := range files {
		if kindOf(p) != "" {
			it := itemAt(p, files)
			items[key(it)] = it
		}
	}
	return items
}

// key is an item without its name, as a need refers to it.
func key(it interfaces.ReleaseItem) interfaces.ReleaseItem {
	return interfaces.ReleaseItem{Kind: it.Kind, ID: it.ID}
}
