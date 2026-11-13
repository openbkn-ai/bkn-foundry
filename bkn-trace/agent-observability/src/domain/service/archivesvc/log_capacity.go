// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package archivesvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

type LogLimits struct {
	MaxCandidates   int `json:"max_candidates"`
	MaxPayloadBytes int `json:"max_payload_bytes"`
}

func DefaultLogLimits() LogLimits { return LogLimits{10000, 10 << 20} }
func (l LogLimits) Validate() error {
	if l.MaxCandidates < 1 || l.MaxCandidates > 10000 || l.MaxPayloadBytes < 1024 || l.MaxPayloadBytes > 64<<20 {
		return ErrInvalidLogLimits
	}
	return nil
}

var ErrInvalidLogLimits = errors.New("invalid log archive limits")
var ErrLogArchiveCapacity = errors.New("log archive exceeds its effective capacity")

// Failure contains safe operational metadata, never an upstream error/URL.
type Failure struct {
	Stage       string `json:"stage"`
	Unit        string `json:"unit,omitempty"`
	CandidateID string `json:"candidate_id,omitempty"`
	Actual      int64  `json:"actual,omitempty"`
	Limit       int64  `json:"limit,omitempty"`
	AtLeast     bool   `json:"at_least,omitempty"`
}

func (f *Failure) Error() string {
	return fmt.Sprintf("archive stopped at %s: candidate=%s actual=%d limit=%d; source retained; inspect the archive job before retrying", f.Stage, f.CandidateID, f.Actual, f.Limit)
}
func (f *Failure) Unwrap() error { return ErrLogArchiveCapacity }

type logLimitsKey struct{}

func WithLogLimits(ctx context.Context, l LogLimits) context.Context {
	return context.WithValue(ctx, logLimitsKey{}, l)
}
func LogLimitsFromContext(ctx context.Context) LogLimits {
	if l, ok := ctx.Value(logLimitsKey{}).(LogLimits); ok {
		return l
	}
	return DefaultLogLimits()
}

type LogCheckpointPreparer interface {
	// Preparation selects a bounded prefix and durably saves its complete frozen
	// recovery input BEFORE object upload. No deletion is authorized by this alone.
	PrepareLogCheckpoint(context.Context, Job, LogLimits) (Job, error)
}

// FitLogCheckpoint bounds both JSONL and the database recovery representation.
// wireBudget is actual server/client packet capacity less a metadata reserve.
// Twice the JSON length is a conservative SQL escaping bound, also safe for
// binary parameters. This does not depend on the connection's sql_mode.
func FitLogCheckpoint(job Job, l LogLimits, wireBudget int64) (Job, error) {
	if err := l.Validate(); err != nil {
		return Job{}, err
	}
	payloadBytes, wireBytes := int64(0), int64(8)
	count := 0
	for _, c := range job.Candidates {
		if count >= l.MaxCandidates {
			break
		}
		raw, err := json.Marshal(c)
		if err != nil {
			return Job{}, err
		}
		id, err := json.Marshal(c.ID)
		if err != nil {
			return Job{}, err
		}
		payloadCost := int64(len(c.Payload) + 1)
		wireCost := int64(2 * (len(raw) + len(id) + 2))
		failure := &Failure{Stage: "checkpoint_bytes", Unit: "bytes", CandidateID: c.ID, Actual: wireBytes + wireCost, Limit: wireBudget}
		if payloadBytes+payloadCost > int64(l.MaxPayloadBytes) {
			failure.Stage = "payload_bytes"
			failure.Actual = payloadBytes + payloadCost
			failure.Limit = int64(l.MaxPayloadBytes)
		} else if wireBytes+wireCost <= wireBudget {
			payloadBytes += payloadCost
			wireBytes += wireCost
			count++
			continue
		}
		if count == 0 {
			return Job{}, failure
		}
		break
	}
	job.Candidates = append([]Candidate(nil), job.Candidates[:count]...)
	job.CandidateCount = count
	job.CandidateIDs = make([]string, 0, count)
	for _, c := range job.Candidates {
		job.CandidateIDs = append(job.CandidateIDs, c.ID)
	}
	sort.Strings(job.CandidateIDs)
	if job.BatchProgress != nil {
		p := *job.BatchProgress
		job.BatchProgress = &p
		p.CheckpointPrepared = true
		p.Stage = "prepared"
		p.LogLimits = &l
		p.RemainingAtSelection = p.EligibleAtSelection - count
	}
	return job, nil
}

func (store *memoryStore) PrepareLogCheckpoint(ctx context.Context, job Job, l LogLimits) (Job, error) {
	if err := ctx.Err(); err != nil {
		return Job{}, err
	}
	prepared, err := FitLogCheckpoint(job, l, 128<<20)
	if err != nil {
		return Job{}, err
	}
	return prepared, store.Create(prepared)
}

// OperationError identifies the completed/write-read boundary without exposing
// the underlying HTTP error, which may contain a signed storage URL.
type OperationError struct {
	Stage string
	Cause error `json:"-"`
}

func (e *OperationError) Error() string { return "archive operation failed at " + e.Stage }
func (e *OperationError) Unwrap() error { return e.Cause }
