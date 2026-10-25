// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/archivesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

type archiveErrorSource struct {
	err             error
	freezes, purges int
}

func (s *archiveErrorSource) Freeze(context.Context, observabilityvo.ArchiveKind, observabilityvo.ArchiveRange) ([]archivesvc.Candidate, error) {
	s.freezes++
	return nil, s.err
}
func (s *archiveErrorSource) Purge(context.Context, observabilityvo.ArchiveKind, []archivesvc.Candidate) error {
	s.purges++
	return nil
}

func TestArchiveCreateErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		name       string
		err        error
		status     int
		code       string
		authorized bool
	}{
		{"payload-budget", archivesvc.ErrArchivePayloadTooLarge, 409, "archive_incomplete_candidates", true},
		{"incomplete", fmt.Errorf("internal detail: %w", archivesvc.ErrIncompleteCandidates), 409, "archive_incomplete_candidates", true},
		{"busy", archivesvc.ErrArchiveBusy, 409, "archive_busy", true},
		{"unknown", fmt.Errorf("internal detail"), 500, "archive_failed", true},
		{"unauthorized", archivesvc.ErrIncompleteCandidates, 403, "archive_access_denied", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			roles := []string{"user"}
			if tc.authorized {
				roles = []string{"admin"}
			}
			auth := NewEvidenceHandlerWithSecurityConfig(nil, EvidenceHandlerSecurityConfig{
				HydraAdminURL: "http://hydra.test", QueryHTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"active":true,"sub":"user-1","client_id":"cli","ext":{"visitor_type":"user"}}`))}, nil
				})},
				AuthorizationScopeResolver: &fakeAccessScopeResolver{profile: evidencevo.AccessProfile{ActorID: "user-1", EffectiveSubjectID: "user-1", AccountActive: true, Roles: roles}},
			})
			source := &archiveErrorSource{err: tc.err}
			// Both adapters must preserve errors.Is identity through their wrapping layers.
			routed := archivesvc.Router{Log: archivesvc.TraceBundleSource{Core: source}}
			jobs := archivesvc.NewMemoryStore()
			h := NewArchiveHandler(archivesvc.New(jobs, routed, nil, archivesvc.Options{}), auth)
			req := httptest.NewRequest(http.MethodPost, "/archives", strings.NewReader(`{}`))
			req.Header.Set("Authorization", "Bearer test-token")
			rec := httptest.NewRecorder()
			h.Create(observabilityvo.ArchiveKindLog)(rec, req)
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.code) {
				t.Fatalf("unexpected response %d %s", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "internal detail") {
				t.Fatal("internal error leaked")
			}
			if tc.name == "payload-budget" && !strings.Contains(rec.Body.String(), "8 MiB") {
				t.Fatal("missing archive budget explanation")
			}
			if tc.name == "incomplete" && !strings.Contains(rec.Body.String(), "this batch was not started; earlier batches remain recorded") {
				t.Fatal("missing safe failure explanation")
			}
			if source.purges != 0 {
				t.Fatal("error must not purge")
			}
			if _, ok := jobs.Latest(observabilityvo.ArchiveKindLog); ok {
				t.Fatal("error must not create a job")
			}
			if !tc.authorized && source.freezes != 0 {
				t.Fatal("unauthorized request queried archive source")
			}
		})
	}
}
