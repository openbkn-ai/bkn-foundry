// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// maxPartRunes bounds the document text one ask of the concepts run reads, in characters. The
// concepts run quotes what it extracts, so its answer grows with what it reads: read whole, a long
// PRD made answers longer than a model writes at once, cut off and never valid JSON.
var maxPartRunes = 24000

// documentPart is a part of the documents a run reads: whole documents, or the blocks of one, in
// order, with a key that names it as long as its text is the same.
type documentPart struct {
	key  string
	docs []*interfaces.RequirementDocument
}

// splitDocuments puts the documents' blocks, in order, into parts of at most maxPartRunes
// characters: short documents share a part, a long one is split between blocks. A block longer
// than that is a part of its own.
func splitDocuments(docs []*interfaces.RequirementDocument) []documentPart {
	parts := []documentPart{}
	var current []*interfaces.RequirementDocument
	size := 0
	closePart := func() {
		if len(current) > 0 {
			parts = append(parts, documentPart{key: partKey(current), docs: current})
		}
		current, size = nil, 0
	}
	for _, d := range docs {
		var slice *interfaces.RequirementDocument
		for _, b := range d.Blocks {
			runes := b.End - b.Start
			if size > 0 && size+runes > maxPartRunes {
				closePart()
				slice = nil
			}
			if slice == nil {
				part := *d
				part.Blocks = nil
				slice = &part
				current = append(current, slice)
			}
			slice.Blocks = append(slice.Blocks, b)
			size += runes
		}
	}
	closePart()
	return parts
}

// partKey names a part by its documents' ids, contents and blocks: a part of a document that
// changed is another part.
func partKey(docs []*interfaces.RequirementDocument) string {
	h := sha256.New()
	for _, d := range docs {
		h.Write([]byte(d.ID + "\x00" + d.SHA256 + "\x00"))
		if len(d.Blocks) > 0 {
			h.Write([]byte(d.Blocks[0].ID + "\x00" + d.Blocks[len(d.Blocks)-1].ID + "\x00"))
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// pendingParts are the parts among parts whose keys are pending, in order.
func pendingParts(parts []documentPart, pending []string) []documentPart {
	out := []documentPart{}
	for _, p := range parts {
		if slices.Contains(pending, p.key) {
			out = append(out, p)
		}
	}
	return out
}

// sameDocuments tells whether two runs read the same documents, by id and content: the type the
// concepts run suggests for a document does not make it another.
func sameDocuments(a, b []interfaces.RequirementInputDocument) bool {
	return slices.EqualFunc(a, b, func(x, y interfaces.RequirementInputDocument) bool {
		return x.ID == y.ID && x.SHA256 == y.SHA256
	})
}

// partKeys are the keys of parts, in order.
func partKeys(parts []documentPart) []string {
	keys := make([]string, 0, len(parts))
	for _, p := range parts {
		keys = append(keys, p.key)
	}
	return keys
}
