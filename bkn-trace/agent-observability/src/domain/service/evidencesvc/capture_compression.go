// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
)

const captureCompressedVersion = "internal-capture-v2-gzip-bodies"

// Only nonempty bodies are gzip members. JSON null and the empty byte string
// retain their distinct original representations. No source JSON is normalized.
func compressCapturePackage(ctx context.Context, p capturePackage, maxBytes int) ([]byte, error) {
	metadata, err := json.Marshal(p.Manifest)
	if err != nil {
		return nil, err
	}
	remaining := maxBytes - len(metadata)
	if remaining < 0 {
		return nil, ErrCapturePackageBudget
	}
	for _, body := range p.Bodies {
		if len(body) > remaining {
			return nil, ErrCapturePackageBudget
		}
		remaining -= len(body)
	}
	p.Version = captureCompressedVersion
	p.Bodies = append([][]byte(nil), p.Bodies...)
	for i, body := range p.Bodies {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(body) == 0 {
			continue
		}
		var out bytes.Buffer
		w := gzip.NewWriter(&out)
		for offset := 0; offset < len(body); {
			if err := ctx.Err(); err != nil {
				_ = w.Close()
				return nil, err
			}
			end := min(offset+(32<<10), len(body))
			if _, err := w.Write(body[offset:end]); err != nil {
				_ = w.Close()
				return nil, err
			}
			offset = end
		}
		if err := w.Close(); err != nil {
			return nil, err
		}
		p.Bodies[i] = out.Bytes()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxBytes {
		return nil, ErrCapturePackageBudget
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return raw, nil
}

func decompressCaptureBodies(p *capturePackage, maxBytes int) error {
	metadata, err := json.Marshal(p.Manifest)
	if err != nil {
		return err
	}
	remaining := maxBytes - len(metadata)
	if remaining < 0 {
		return ErrCapturePackageBudget
	}
	for i, body := range p.Bodies {
		if len(body) == 0 {
			continue
		}
		input := bytes.NewReader(body)
		r, err := gzip.NewReader(input)
		if err != nil {
			return errors.New("invalid compressed capture body")
		}
		r.Multistream(false)
		decoded, readErr := io.ReadAll(io.LimitReader(r, int64(remaining)+1))
		closeErr := r.Close()
		if readErr != nil || closeErr != nil {
			return errors.New("invalid compressed capture body")
		}
		if len(decoded) > remaining {
			return ErrCapturePackageBudget
		}
		if input.Len() != 0 {
			return errors.New("compressed capture body has trailing data")
		}
		remaining -= len(decoded)
		p.Bodies[i] = decoded
	}
	return nil
}
