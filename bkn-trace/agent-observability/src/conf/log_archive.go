// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package conf

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/archivesvc"
)

// ReadLogArchiveLimits snapshots owner-managed configuration once per batch.
// Kubernetes mounted ConfigMap directories refresh without restarting the process;
// subPath mounts must not be used because Kubernetes does not update them.
func ReadLogArchiveLimits(context.Context) (archivesvc.LogLimits, error) {
	l := archivesvc.DefaultLogLimits()
	for name, target := range map[string]*int{"BKN_OBSERVABILITY_LOG_ARCHIVE_MAX_CANDIDATES": &l.MaxCandidates, "BKN_OBSERVABILITY_LOG_ARCHIVE_MAX_PAYLOAD_BYTES": &l.MaxPayloadBytes} {
		if value, ok := os.LookupEnv(name); ok {
			n, err := strconv.Atoi(value)
			if err != nil {
				return l, fmt.Errorf("%w: %s", archivesvc.ErrInvalidLogLimits, name)
			}
			*target = n
		}
	}
	if file := os.Getenv("BKN_OBSERVABILITY_LOG_ARCHIVE_LIMITS_FILE"); file != "" {
		f, err := os.Open(file)
		if err != nil {
			return l, archivesvc.ErrInvalidLogLimits
		}
		defer func() { _ = f.Close() }()
		raw, err := io.ReadAll(io.LimitReader(f, 4097))
		if err != nil || len(raw) > 4096 {
			return l, archivesvc.ErrInvalidLogLimits
		}
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) < 2 || trimmed[0] != '{' {
			return l, archivesvc.ErrInvalidLogLimits
		}
		var override struct {
			MaxCandidates   *int `json:"max_candidates"`
			MaxPayloadBytes *int `json:"max_payload_bytes"`
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if d.Decode(&override) != nil {
			return l, archivesvc.ErrInvalidLogLimits
		}
		var tail any
		if d.Decode(&tail) != io.EOF {
			return l, archivesvc.ErrInvalidLogLimits
		}
		if override.MaxCandidates != nil {
			l.MaxCandidates = *override.MaxCandidates
		}
		if override.MaxPayloadBytes != nil {
			l.MaxPayloadBytes = *override.MaxPayloadBytes
		}
	}
	return l, l.Validate()
}
