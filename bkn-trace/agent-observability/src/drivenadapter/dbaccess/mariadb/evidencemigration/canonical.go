// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package evidencemigration

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"unicode/utf8"
)

// frozenEntry is the C1 fixed 13-field manifest-entry domain, not a general JSON value.
type frozenEntry struct{ Classification, ClassificationReason, EventID, ManifestID, PayloadHash, ProducerEpoch, ProducerID, ProducerSequence, ProducerStreamID, SourcePrimaryKey, SourceService, SourceStatus, SourceTable string }

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func canonicalEntry(e frozenEntry) (map[string]any, error) {
	for _, value := range []string{e.Classification, e.ClassificationReason, e.EventID, e.ManifestID, e.PayloadHash, e.ProducerEpoch, e.ProducerID, e.ProducerSequence, e.ProducerStreamID, e.SourcePrimaryKey, e.SourceService, e.SourceStatus, e.SourceTable} {
		for _, r := range value {
			if r < 0x20 || r > 0x7e {
				return nil, errors.New("manifest entry is outside C1 ASCII subset")
			}
		}
	}
	return map[string]any{"classification": e.Classification, "classification_reason": e.ClassificationReason, "event_id": nullable(e.EventID), "manifest_id": e.ManifestID, "payload_hash": nullable(e.PayloadHash), "producer_epoch": nullable(e.ProducerEpoch), "producer_id": nullable(e.ProducerID), "producer_sequence": nullable(e.ProducerSequence), "producer_stream_id": nullable(e.ProducerStreamID), "source_primary_key": e.SourcePrimaryKey, "source_service": e.SourceService, "source_status": e.SourceStatus, "source_table": e.SourceTable}, nil
}

var entryKeys = []string{"classification", "classification_reason", "event_id", "manifest_id", "payload_hash", "producer_epoch", "producer_id", "producer_sequence", "producer_stream_id", "source_primary_key", "source_service", "source_status", "source_table"}

func quote(b []byte, s string) ([]byte, error) {
	if !utf8.ValidString(s) {
		return nil, errors.New("manifest entry string is not valid UTF-8")
	}
	b = append(b, '"')
	const hex = "0123456789abcdef"
	for _, c := range []byte(s) {
		switch c {
		case '"', '\\':
			b = append(b, '\\', c)
		case '\b':
			b = append(b, '\\', 'b')
		case '\t':
			b = append(b, '\\', 't')
		case '\n':
			b = append(b, '\\', 'n')
		case '\f':
			b = append(b, '\\', 'f')
		case '\r':
			b = append(b, '\\', 'r')
		default:
			if c < 0x20 {
				b = append(b, '\\', 'u', '0', '0', hex[c>>4], hex[c&15])
			} else {
				b = append(b, c)
			}
		}
	}
	return append(b, '"'), nil
}
func appendEntry(b []byte, e frozenEntry) ([]byte, error) {
	m, _ := canonicalEntry(e)
	b = append(b, '{')
	for i, k := range entryKeys {
		if i > 0 {
			b = append(b, ',')
		}
		b, _ = quote(b, k)
		b = append(b, ':')
		if m[k] == nil {
			b = append(b, "null"...)
		} else {
			var err error
			b, err = quote(b, m[k].(string))
			if err != nil {
				return nil, err
			}
		}
	}
	return append(b, '}'), nil
}
func entriesDigest(entries []frozenEntry) (string, error) {
	ordered := append([]frozenEntry(nil), entries...)
	sort.Slice(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		for _, p := range [][2]string{{a.SourceService, b.SourceService}, {a.SourceTable, b.SourceTable}, {a.SourcePrimaryKey, b.SourcePrimaryKey}} {
			if p[0] != p[1] {
				return strings.Compare(p[0], p[1]) < 0
			}
		}
		return false
	})
	raw := []byte{'['}
	for i, e := range ordered {
		if i > 0 {
			raw = append(raw, ',')
		}
		var err error
		raw, err = appendEntry(raw, e)
		if err != nil {
			return "", err
		}
	}
	raw = append(raw, ']')
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
