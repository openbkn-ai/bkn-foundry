// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package ossgatewayarchive

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/archivesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

func TestTraceWriteRejectsBudgetBeforeStorageRequest(t *testing.T) {
	for _, name := range []string{"single", "batch", "count"} {
		t.Run(name, func(t *testing.T) {
			client := New(Config{StorageID: "archive"})
			requests := 0
			client.http.Transport = roundTripper(func(*http.Request) (*http.Response, error) {
				requests++
				return nil, errors.New("must not call storage")
			})
			var candidates []archivesvc.Candidate
			switch name {
			case "single":
				candidates = []archivesvc.Candidate{{Payload: make([]byte, archivesvc.MaxTraceArchiveCandidateBytes+1)}}
			case "batch":
				payload := make([]byte, archivesvc.MaxTraceArchiveCandidateBytes)
				candidates = []archivesvc.Candidate{{Payload: payload}, {Payload: payload}, {Payload: payload}}
			case "count":
				candidates = make([]archivesvc.Candidate, archivesvc.MaxTraceArchiveCandidates+1)
			}
			_, err := client.WriteAndVerify(context.Background(), archivesvc.Job{Kind: observabilityvo.ArchiveKindTrace}, candidates)
			if !errors.Is(err, archivesvc.ErrArchivePayloadTooLarge) || requests != 0 {
				t.Fatalf("budget not enforced: %v requests=%d", err, requests)
			}
		})
	}
}

func TestTraceWriteVerificationReadIsBounded(t *testing.T) {
	client := New(Config{StorageID: "archive"})
	client.http.Transport = roundTripper(func(r *http.Request) (*http.Response, error) {
		body := ""
		if r.Method == http.MethodGet {
			body = `{"data":{"method":"PUT","url":"http://objects/data"}}`
			if strings.Contains(r.URL.Path, "download") {
				body = `{"data":{"method":"GET","url":"http://objects/data"}}`
			}
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	observed := &countingArchiveReader{Reader: strings.NewReader(strings.Repeat("x", 1024))}
	client.stream.Transport = roundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: observed}, nil
	})
	_, err := client.WriteAndVerify(context.Background(), archivesvc.Job{ID: "job", Kind: observabilityvo.ArchiveKindTrace}, []archivesvc.Candidate{{ID: "i", Payload: []byte(`{}`)}})
	if err == nil || observed.n != 4 {
		t.Fatalf("verification downloaded beyond frozen length+1: %v bytes=%d", err, observed.n)
	}
}

type countingArchiveReader struct {
	io.Reader
	n int
}

func (r *countingArchiveReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.n += n
	return n, err
}
func (*countingArchiveReader) Close() error { return nil }
