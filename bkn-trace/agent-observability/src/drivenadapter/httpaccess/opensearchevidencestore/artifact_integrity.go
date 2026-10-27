// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package opensearchevidencestore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iartifactstore"
)

const integrityObjectBytes = 32 << 20
const integrityResponseBytes = 8 << 20
const integrityTokenBytes = 8 << 20

// Historical rechecks process one object at a time: small metadata, an exact
// version stream, and no retained full content. Existing capture stays bounded
// by its original complete-response policy.
func (s *Store) VerifyArtifactForIntegrity(ctx context.Context, id string, scope evidencevo.QueryScope) (iartifactstore.IntegrityReadResult, error) {
	var result iartifactstore.IntegrityReadResult
	if !evidencevo.ValidArtifactID(id) {
		return result, errors.New("invalid integrity artifact ID")
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	document, n, err := s.client.GetDocumentBounded(ctx, s.artifactIndex(), id, integrityResponseBytes, "content_json")
	result.ReadBytes = n
	if errors.Is(err, opensearch.ErrDocumentNotFound) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	artifact, err := decodeArtifactForCapture(document.Source)
	if err != nil {
		return result, err
	}
	if artifact.ArtifactID != id {
		return result, iartifactstore.ErrCaptureIdentityMismatch
	}
	result.Exists = true
	if !evidencevo.MatchesArtifactScope(artifact, scope) {
		return result, nil
	}
	if artifact.SchemaVersion != evidencevo.ArtifactContractVersion || artifact.SnapshotRef != "" || document.Version <= 0 {
		return result, errors.New("unsupported integrity artifact metadata")
	}
	stream, err := s.client.OpenContentJSON(ctx, s.artifactIndex(), id, document.Version, integrityObjectBytes-n)
	if err != nil {
		return result, err
	}
	defer func() { _ = stream.Close() }()
	digest := sha256.New()
	length, escaped, err := hashCanonicalIntegrityJSON(stream, digest)
	result.ReadBytes += stream.ReadBytes()
	if err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if "sha256:"+hex.EncodeToString(digest.Sum(nil)) != artifact.ContentHash {
		return result, errors.New("integrity artifact hash mismatch")
	}
	result.Artifact = artifact
	result.Found = true
	result.CanonicalLength = length
	result.HTMLEscapedLength = escaped
	return result, nil
}

// Token decoding avoids retaining a complete artifact. Object keys are already
// sorted by the producer's encoding/json; reject duplicates/out-of-order keys
// rather than treating a different byte stream as verified canonical content.
func hashCanonicalIntegrityJSON(reader io.Reader, writer io.Writer) (int, int, error) {
	decoder := json.NewDecoder(&integrityTokenReader{reader: reader})
	decoder.UseNumber()
	length, escaped := 0, 0
	emit := func(raw []byte) error {
		length += len(raw)
		escaped += len(raw)
		for _, b := range raw {
			if b == '<' || b == '>' || b == '&' {
				escaped += 5
			}
		}
		_, err := writer.Write(raw)
		return err
	}
	var value func(int) error
	value = func(depth int) error {
		if depth > 128 {
			return errors.New("integrity JSON nesting limit")
		}
		token, err := decoder.Token()
		if depth == 0 && token == nil && err == nil {
			return errors.New("integrity artifact content is null")
		}
		if err != nil {
			return err
		}
		if delimiter, ok := token.(json.Delim); ok {
			if delimiter != '{' && delimiter != '[' {
				return errors.New("invalid integrity JSON delimiter")
			}
			if err = emit([]byte{byte(delimiter)}); err != nil {
				return err
			}
			first := true
			lastKey := ""
			for decoder.More() {
				if !first {
					if err = emit([]byte(",")); err != nil {
						return err
					}
				}
				if delimiter == '{' {
					keyToken, err := decoder.Token()
					if err != nil {
						return err
					}
					key, ok := keyToken.(string)
					if !ok || (!first && key <= lastKey) {
						return errors.New("integrity JSON keys are not canonical")
					}
					lastKey = key
					if err = emitIntegrityToken(key, emit); err != nil {
						return err
					}
					if err = emit([]byte(":")); err != nil {
						return err
					}
				}
				if err = value(depth + 1); err != nil {
					return err
				}
				first = false
			}
			end, err := decoder.Token()
			if err != nil {
				return err
			}
			want := json.Delim(']')
			if delimiter == '{' {
				want = '}'
			}
			if end != want {
				return errors.New("invalid integrity JSON closing delimiter")
			}
			return emit([]byte{byte(want)})
		}
		return emitIntegrityToken(token, emit)
	}
	if err := value(0); err != nil {
		return 0, 0, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return 0, 0, errors.New("integrity JSON trailing data")
	}
	return length, escaped, nil
}
func emitIntegrityToken(token any, emit func([]byte) error) error {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(token); err != nil {
		return err
	}
	raw := bytes.TrimSuffix(buffer.Bytes(), []byte("\n"))
	if len(raw) > integrityTokenBytes {
		return errors.New("integrity JSON token limit")
	}
	return emit(raw)
}

// Bound a token before Decoder can grow its buffer to the whole content body.
// Decoder remains responsible for JSON grammar, Unicode and number validation.
type integrityTokenReader struct {
	reader           io.Reader
	inString, escape bool
	size             int
}

func (r *integrityTokenReader) Read(p []byte) (int, error) {
	if len(p) > 32<<10 {
		p = p[:32<<10]
	}
	n, err := r.reader.Read(p)
	for _, b := range p[:n] {
		if r.inString {
			r.size++
			if r.escape {
				r.escape = false
			} else if b == '\\' {
				r.escape = true
			} else if b == '"' {
				r.inString = false
				r.size = 0
			}
		} else if b == '"' {
			r.inString = true
			r.size = 1
		} else if b == ',' || b == ':' || b == '[' || b == ']' || b == '{' || b == '}' || b == ' ' || b == '\n' || b == '\r' || b == '\t' {
			r.size = 0
		} else {
			r.size++
		}
		if r.size > integrityTokenBytes {
			return 0, fmt.Errorf("integrity JSON token exceeds %d bytes", integrityTokenBytes)
		}
	}
	return n, err
}
