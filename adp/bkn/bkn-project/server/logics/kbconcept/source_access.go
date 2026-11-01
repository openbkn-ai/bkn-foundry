// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbconcept

import (
	"context"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// DraftSourceIDs identifies KBs whose document evidence is present in a draft.
// Saved locations/quotes are not an authorization grant for a later reader.
func DraftSourceIDs(items interfaces.DraftItems) []string {
	ids := []string{}
	seen := map[string]bool{}
	add := func(quotes []interfaces.DraftSourceCitation) {
		for _, q := range quotes {
			if q.KnowledgeBaseID != "" && !seen[q.KnowledgeBaseID] {
				ids = append(ids, q.KnowledgeBaseID)
				seen[q.KnowledgeBaseID] = true
			}
		}
	}
	for _, object := range items.ObjectTypes {
		if object == nil {
			continue
		}
		if object.Document != nil {
			add(object.Document.Citations)
		}
		for _, p := range object.Properties {
			if p != nil && p.Extraction != nil {
				add(p.Extraction.Examples)
			}
		}
	}
	for _, relation := range items.RelationTypes {
		if relation != nil && relation.Document != nil {
			add(relation.Document.Citations)
		}
	}
	return ids
}
func CheckDraftSources(ctx context.Context, source kb.Source, items interfaces.DraftItems) error {
	for _, id := range DraftSourceIDs(items) {
		if source == nil {
			return kb.ErrUnavailable
		}
		if _, err := source.SummarizeFiles(ctx, kb.SourceScope{KnowledgeBaseID: id}, kb.FileQuery{}); err != nil {
			return err
		}
	}
	return nil
}
