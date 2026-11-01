// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledgebase

import (
	"context"
	"encoding/json"
	"errors"
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
