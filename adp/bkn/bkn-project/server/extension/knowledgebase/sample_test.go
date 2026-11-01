// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledgebase

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"strings"
	"testing"
)

func TestSampleSourceUnavailableAndWireIdentity(t *testing.T) {
	var reader SampleSource = UnavailableSource{}
	if _, err := reader.ReadSampleChunks(context.Background(), nil, PageQuery{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("community batch read must be unavailable: %v", err)
	}
	var page SamplePage
	if err := json.Unmarshal([]byte(`{"entries":[{"kb_id":"2","file_id":"41","content_version_id":"8","chunk_id":"9","index":0,"text":"甲😀","spans":[{"block_id":"p1b1","start":0,"end":2}],"located":"exact"}],"next_cursor":null}`), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 || page.Entries[0].KnowledgeBaseID != "2" || page.Entries[0].FileID != "41" || page.Entries[0].ContentVersionID != "8" || page.Entries[0].ChunkID != "9" || page.Entries[0].Text != "甲😀" {
		t.Fatalf("batch identity must survive decoding: %#v", page)
	}
}

func TestConceptWireUsesBusinessSchemaAndDoesNotSerializeOperatorContext(t *testing.T) {
	raw, err := json.Marshal(ConceptRequest{NamingLanguage: "zh-CN", Caller: interfaces.AccountInfo{ID: "synthetic-operator", Type: "user"}, ExistingObjects: []*interfaces.DraftObjectType{{ID: "existing-private-context"}}, Samples: []Chunk{{ID: "9", Text: "甲", Citation: Citation{KnowledgeBaseID: "2", VersionRef: VersionRef{FileID: "41", ContentVersionID: "8"}, BlockID: "b1", Start: 0, End: 1, Quote: "甲"}}}})
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Contains(text, "synthetic-operator") || strings.Contains(text, "existing-private-context") || !strings.Contains(text, `"naming_language"`) || !strings.Contains(text, `"content_version_id":"8"`) || !strings.Contains(text, `"block_id":"b1"`) {
		t.Fatalf("unsafe concept request wire: %s", text)
	}
	var proposal ConceptProposal
	if err := json.Unmarshal([]byte(`{"object_types":[{"id":"contract_doc","name":"合同","document":{"role":"entity","reason":"sample"}}],"relation_types":[]}`), &proposal); err != nil || len(proposal.ObjectTypes) != 1 || proposal.ObjectTypes[0].Document.Role != "entity" {
		t.Fatal("structured model response did not decode", err)
	}
}
