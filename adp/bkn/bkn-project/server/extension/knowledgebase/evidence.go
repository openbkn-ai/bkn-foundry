// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledgebase

import "context"

// EvidenceSource reads native version-bound locations independently of chunks.
// Standalone images need no OCR. Every page and text read rechecks permission.
type EvidenceSource interface {
	ReadBlocks(context.Context, SampleVersion, PageQuery) (BlockPage, error)
	ReadMedia(context.Context, SampleVersion, PageQuery) (MediaPage, error)
	ReadText(context.Context, SampleVersion, Span) (string, error)
}

type EvidenceBlock struct {
	SampleVersion
	BlockID      string `json:"block_id"`
	Ordinal      int    `json:"ordinal"`
	PageNo       *int   `json:"page_no"`
	ContentType  string `json:"content_type"`
	ElementIndex *int   `json:"element_index"`
	Start        int    `json:"source_start"`
	End          int    `json:"source_end"`
}
type BlockPage struct {
	Entries    []EvidenceBlock `json:"entries"`
	NextCursor string          `json:"next_cursor"`
}

// MediaID identifies a resource; OccurrenceID identifies one native use of it.
// Neither is a capability or a storage URL. BBox retains native coordinates.
type EvidenceMedia struct {
	SampleVersion
	MediaID      string    `json:"media_id"`
	OccurrenceID string    `json:"occurrence_id"`
	ImageRef     string    `json:"image_ref"`
	Kind         string    `json:"kind"`
	MimeType     string    `json:"mime_type"`
	PageNo       *int      `json:"page_no"`
	BBox         []float64 `json:"bbox"`
	BlockIDs     []string  `json:"block_ids"`
}
type MediaPage struct {
	Entries    []EvidenceMedia `json:"entries"`
	NextCursor string          `json:"next_cursor"`
}
