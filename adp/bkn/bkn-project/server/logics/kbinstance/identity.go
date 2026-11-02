// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package kbinstance validates document extraction results and derives their
// server-owned identities. It does not persist extracted instances.
package kbinstance

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

var ErrInvalidIdentity = errors.New("invalid document instance identity")

// identityDigest uses length-prefixed UTF-8 components, not ambiguous separators.
// Domains version the encoding independently from an Agent/extraction version.
func identityDigest(domain string, parts ...string) (string, error) {
	h := sha256.New()
	for _, part := range append([]string{domain}, parts...) {
		if !utf8.ValidString(part) || strings.TrimSpace(part) == "" {
			return "", ErrInvalidIdentity
		}
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(part)))
		_, _ = h.Write(size[:])
		_, _ = h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// DocumentID depends only on native KB/file identity. A parse/content revision,
// physical block id or publication network id must never enter this identity.
func DocumentID(knowledgeBaseID, fileID string) (string, error) {
	return identityDigest("bkn-document-v1", knowledgeBaseID, fileID)
}

// SentenceID follows D6. networkID is the logical project lineage network, not
// a newly published release's network id. Callers derive ordinal by sorting the
// business identities within the same normalized sentence, never model order.
// Only canonical Unicode and whitespace are normalized: case, punctuation and
// full-width characters remain distinct so meaningful claims cannot collapse.
// The original located text is preserved separately; normalization changes no
// native source offsets and never replaces the evidence text.
func SentenceID(networkID, objectTypeID, documentID, sentence string, ordinal uint32) (string, error) {
	if !utf8.ValidString(sentence) {
		return "", ErrInvalidIdentity
	}
	normalized := norm.NFC.String(strings.Join(strings.Fields(sentence), " "))
	return identityDigest("bkn-sentence-instance-v1", networkID, objectTypeID, documentID, normalized, strconv.FormatUint(uint64(ordinal), 10))
}

// FileObjectID retains separate claims for each business object and document.
// It never makes a later file overwrite the object claimed by an earlier file.
func FileObjectID(businessIdentity, documentID string) (string, error) {
	return identityDigest("bkn-file-object-v1", businessIdentity, documentID)
}

// DerivedChangeID preserves the ordered before/after commitments from D6.
func DerivedChangeID(beforeID, afterID string) (string, error) {
	return identityDigest("bkn-derived-change-v1", beforeID, afterID)
}
