// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"slices"
	"unicode/utf8"
)

// EvidenceRows contains Dataset rows keyed by the fixed model role. This value
// exists only during a file build; process persistence must never serialize it.
type EvidenceRows struct {
	Objects map[string][]map[string]any
	Edges   []EvidenceEdge
}
type EvidenceEdge struct{ SourceRole, TargetRole, SourceID, TargetID string }

// BuildEvidenceRows keeps native media resources distinct from occurrences and
// preserves every block association. The original-text switch is a write gate:
// when off, only the file locator is materialized, even if callers supplied text.
func BuildEvidenceRows(ref kb.SampleVersion, name string, includeOriginal bool, blocks []kb.EvidenceBlock, media []kb.EvidenceMedia, text map[string]string) (*EvidenceRows, error) {
	document, err := DocumentID(ref.KnowledgeBaseID, ref.FileID)
	if err != nil || ref.ContentVersionID == "" {
		return nil, ErrInvalidIdentity
	}
	result := &EvidenceRows{Objects: map[string][]map[string]any{}, Edges: []EvidenceEdge{}}
	result.Objects["evidence_document"] = []map[string]any{{"_id": document, "knowledge_base_id": ref.KnowledgeBaseID, "file_id": ref.FileID, "content_version_id": ref.ContentVersionID, "name": name}}
	if !includeOriginal {
		return result, nil
	}
	blockIDs := map[string]string{}
	for _, block := range blocks {
		if block.SampleVersion != ref || block.BlockID == "" || blockIDs[block.BlockID] != "" || block.Start < 0 || block.End < block.Start {
			return nil, ErrInvalidIdentity
		}
		id, err := identityDigest("bkn-source-block-v1", document, ref.ContentVersionID, block.BlockID)
		if err != nil {
			return nil, err
		}
		quote, found := text[block.BlockID]
		// Empty image/layout blocks have no text interval and require no text read.
		if !found && block.End != block.Start {
			return nil, ErrInvalidIdentity
		}
		if !validEvidenceText(quote, block.End-block.Start) {
			return nil, ErrInvalidIdentity
		}
		blockIDs[block.BlockID] = id
		result.Objects["evidence_block"] = append(result.Objects["evidence_block"], map[string]any{"_id": id, "document_id": document, "content_version_id": ref.ContentVersionID, "block_id": block.BlockID, "source_start": block.Start, "source_end": block.End, "source_quote": quote})
	}
	resources := map[string]kb.EvidenceMedia{}
	occurrences := map[string]bool{}
	for _, entry := range media {
		if entry.SampleVersion != ref || entry.MediaID == "" || entry.OccurrenceID == "" || entry.ImageRef != entry.OccurrenceID || occurrences[entry.OccurrenceID] {
			return nil, ErrInvalidIdentity
		}
		occurrences[entry.OccurrenceID] = true
		resourceID, err := identityDigest("bkn-image-resource-v1", document, ref.ContentVersionID, entry.MediaID)
		if err != nil {
			return nil, err
		}
		occurrenceID, err := identityDigest("bkn-image-occurrence-v1", document, ref.ContentVersionID, entry.OccurrenceID)
		if err != nil {
			return nil, err
		}
		if previous, exists := resources[entry.MediaID]; exists {
			if previous.MimeType != entry.MimeType || previous.Kind != entry.Kind {
				return nil, ErrInvalidIdentity
			}
		} else {
			resources[entry.MediaID] = entry
			result.Objects["evidence_media"] = append(result.Objects["evidence_media"], map[string]any{"_id": resourceID, "document_id": document, "content_version_id": ref.ContentVersionID, "media_id": entry.MediaID, "mime_type": entry.MimeType})
		}
		ids := slices.Clone(entry.BlockIDs)
		slices.Sort(ids)
		primary := ""
		seen := map[string]bool{}
		for _, block := range ids {
			id := blockIDs[block]
			if id == "" || seen[block] {
				return nil, ErrInvalidIdentity
			}
			seen[block] = true
			if primary == "" {
				primary = block
			}
			result.Edges = append(result.Edges, EvidenceEdge{SourceRole: "evidence_image", TargetRole: "evidence_block", SourceID: occurrenceID, TargetID: id})
		}
		result.Objects["evidence_image"] = append(result.Objects["evidence_image"], map[string]any{"_id": occurrenceID, "document_id": document, "content_version_id": ref.ContentVersionID, "block_id": primary, "image_ref": entry.ImageRef})
		result.Edges = append(result.Edges, EvidenceEdge{SourceRole: "evidence_media", TargetRole: "evidence_image", SourceID: resourceID, TargetID: occurrenceID})
	}
	// Stable output order also makes a repeated file's write diff independent of
	// parser pagination order, without changing the native location identifiers.
	for role, rows := range result.Objects {
		slices.SortFunc(rows, func(a, b map[string]any) int {
			left, right := a["_id"].(string), b["_id"].(string)
			if left < right {
				return -1
			}
			if left > right {
				return 1
			}
			return 0
		})
		result.Objects[role] = rows
	}
	slices.SortFunc(result.Edges, func(a, b EvidenceEdge) int {
		left, right := a.SourceRole+"/"+a.SourceID+"/"+a.TargetID, b.SourceRole+"/"+b.SourceID+"/"+b.TargetID
		if left < right {
			return -1
		}
		if left > right {
			return 1
		}
		return 0
	})
	return result, nil
}

func validEvidenceText(text string, length int) bool {
	return utf8.ValidString(text) && utf8.RuneCountInString(text) == length
}
