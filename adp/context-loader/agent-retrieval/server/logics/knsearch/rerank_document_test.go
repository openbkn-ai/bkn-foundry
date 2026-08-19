// Copyright openbkn.ai
//
// Licensed under the OpenBKN License.
// See the LICENSE file in the project root for details.

package knsearch

import (
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// A row shaped like the ones that defeated the reranker in practice: the fields carrying the
// meaning ("summary") sort after a pile of metadata, and one early metadata field is a long blob.
func noisyNode() *interfaces.KnSearchNode {
	return &interfaces.KnSearchNode{
		ObjectTypeID: "countermeasure",
		InstanceName: "Alternative 6",
		Properties: map[string]any{
			"attributes":      strings.Repeat("元数据噪声 ", 400),
			"class_id":        "countermeasure",
			"confidence":      "高",
			"document_id":     "doc-001",
			"extraction_type": "explicit",
			"locator":         "Pugh-Graphic!V9",
			"review_status":   "draft",
			"source":          "list_instance",
			"summary":         "注塑机产能受模具变更影响",
			"_instance_id":    "countermeasure-010",
		},
	}
}

// The searchable fields are what recall matched on, so they are what the model has to see.
func TestInstanceRerankDocument_LeadsWithSearchableFields(t *testing.T) {
	doc := instanceRerankDocument(noisyNode(), 200, []searchableField{
		{Name: "summary", HasMatch: true},
	})

	summaryAt := strings.Index(doc, "summary:")
	if summaryAt < 0 {
		t.Fatalf("the searchable field never reached the document: %q", doc)
	}
	for _, metadata := range []string{"attributes:", "class_id:", "locator:"} {
		if at := strings.Index(doc, metadata); at >= 0 && at < summaryAt {
			t.Errorf("%s was written before the searchable field", metadata)
		}
	}
	// The instance name still opens the document.
	if !strings.HasPrefix(doc, "Alternative 6") {
		t.Errorf("expected the instance name first, got %q", doc[:40])
	}
}

// The defect this fixes: with the whole budget spent alphabetically, the field that answers the
// query fell off the end and the model was left judging metadata.
func TestInstanceRerankDocument_SearchableFieldSurvivesTheBudget(t *testing.T) {
	node := noisyNode()

	alphabetical := instanceRerankDocument(node, 3000, nil)
	if strings.Contains(alphabetical, "summary:") {
		t.Skip("budget no longer overflows with this fixture; the regression it guards is gone")
	}

	prioritized := instanceRerankDocument(node, 3000, []searchableField{{Name: "summary", HasMatch: true}})
	if !strings.Contains(prioritized, "注塑机产能受模具变更影响") {
		t.Fatalf("the searchable field is still missing from the document: %q", prioritized)
	}
}

// Same row, same text, every time: the model's score for a row must not depend on map iteration.
func TestInstanceRerankDocument_IsStable(t *testing.T) {
	searchable := []searchableField{{Name: "summary", HasMatch: true}}
	first := instanceRerankDocument(noisyNode(), 200, searchable)
	for i := 0; i < 20; i++ {
		if got := instanceRerankDocument(noisyNode(), 200, searchable); got != first {
			t.Fatalf("document changed between builds:\n%q\n%q", first, got)
		}
	}
}

// A searchable field is written once, not twice.
func TestInstanceRerankDocument_NoDuplicateFields(t *testing.T) {
	doc := instanceRerankDocument(noisyNode(), 200, []searchableField{{Name: "summary", HasMatch: true}})
	if n := strings.Count(doc, "summary:"); n != 1 {
		t.Errorf("expected the searchable field once, got %d", n)
	}
}

// Internal properties stay out however they are ordered.
func TestInstanceRerankDocument_SkipsInternalProperties(t *testing.T) {
	doc := instanceRerankDocument(noisyNode(), 200, []searchableField{{Name: "_instance_id"}})
	if strings.Contains(doc, "countermeasure-010") {
		t.Errorf("internal property leaked into the rerank document: %q", doc)
	}
}
