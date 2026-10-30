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
		missing := []interfaces.ReleaseItem{}
		live, affected := true, false
		// What it needs is what it names and the path that joins them; both are judged alike.
		judge := func(refs []interfaces.RequirementItemRef) []interfaces.ReleaseItem {
			items := []interfaces.ReleaseItem{}
			for _, n := range refs {
				k := interfaces.ReleaseItem{Kind: n.Kind, ID: n.ID}
				it, inBranch := branch[k]
				if !inBranch {
					it = named(k, main)
					missing = append(missing, it)
				}
				items = append(items, it)
				if _, ok := main[k]; !ok {
					live = false
				}
				affected = affected || touched[k]
			}
			return items
		}
		entry := interfaces.ReleaseQuestion{ID: question.ID, Text: question.Text, Needs: judge(question.Needs)}
		if path := judge(question.Path); len(path) > 0 {
			entry.Path = path
		}
		for _, u := range question.Unconnected {
			from := interfaces.ReleaseItem{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: u.From}
			to := interfaces.ReleaseItem{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: u.To}
			entry.Unconnected = append(entry.Unconnected, interfaces.ReleaseUnconnected{
				From: namedIn(from, branch, main), To: namedIn(to, branch, main)})
		}
		switch {
		case live && affected:
			q.Affected = append(q.Affected, entry)
		case !live && len(missing) == 0 && len(entry.Unconnected) == 0:
			q.AnswerableNew = append(q.AnswerableNew, entry)
		}
		if len(missing) > 0 || len(entry.Unconnected) > 0 {
			if len(missing) > 0 {
				entry.Missing = missing
			}
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

// named is an item the branch lacks, named as main has it, else by its id.
func named(k interfaces.ReleaseItem, main map[interfaces.ReleaseItem]interfaces.ReleaseItem) interfaces.ReleaseItem {
	if m, ok := main[k]; ok {
		return m
	}
	k.Name = k.ID
	return k
}

// namedIn is an item named as the branch has it, else as main has it, else by its id.
func namedIn(k interfaces.ReleaseItem, branch map[interfaces.ReleaseItem]interfaces.ReleaseItem,
	main map[interfaces.ReleaseItem]interfaces.ReleaseItem) interfaces.ReleaseItem {
	if b, ok := branch[k]; ok {
		return b
	}
	return named(k, main)
}

// key is an item without its name, as a need refers to it.
func key(it interfaces.ReleaseItem) interfaces.ReleaseItem {
	return interfaces.ReleaseItem{Kind: it.Kind, ID: it.ID}
}
