// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func archiveTestAuth(admin bool) *EvidenceHandler {
	roles := []string{"user"}
	if admin {
		roles = []string{"admin"}
	}
	return NewEvidenceHandlerWithSecurityConfig(nil, EvidenceHandlerSecurityConfig{
		HydraAdminURL: "http://hydra.test", QueryHTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"active":true,"sub":"user-1","client_id":"cli","ext":{"visitor_type":"user"}}`))}, nil
		})},
		AuthorizationScopeResolver: &fakeAccessScopeResolver{profile: evidencevo.AccessProfile{ActorID: "user-1", EffectiveSubjectID: "user-1", AccountActive: true, Roles: roles}},
	})
}

type archiveCountSource struct {
	archiveErrorSource
	count int
}

func (s *archiveCountSource) Count(context.Context, observabilityvo.ArchiveKind, observabilityvo.ArchiveRange) (int, error) {
	s.freezes++
	return s.count, s.err
}

func serveArchive(handler http.HandlerFunc, method, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

// Submission answers only what is known before any batch runs; batch failures
// are recorded on the run by the worker and never reach this response.
func TestArchiveSubmitErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		name       string
		count      int
		err        error
		prepare    func(archivesvc.Store)
		status     int
		code       string
		authorized bool
	}{
		{name: "signed-url", count: 1, err: fmt.Errorf("https://private.test/object?signature=secret"), status: 500, code: "archive_failed", authorized: true},
		{name: "nothing", count: 0, status: 409, code: "archive_nothing_to_do", authorized: true},
		{name: "cleanup", count: 1, status: 409, code: "archive_cleanup_required", authorized: true, prepare: func(store archivesvc.Store) {
			_ = store.Create(archivesvc.Job{ID: "pending", Kind: observabilityvo.ArchiveKindLog, Status: observabilityvo.ArchiveStatusCleanupIncomplete})
		}},
		{name: "busy", count: 1, status: 409, code: "archive_busy", authorized: true, prepare: func(store archivesvc.Store) {
			_ = store.(archivesvc.RunStore).CreateRun(context.Background(), archivesvc.Run{ID: "active", Kind: observabilityvo.ArchiveKindLog, Status: archivesvc.RunRunning})
		}},
		{name: "unauthorized", count: 1, status: 403, code: "archive_access_denied"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := &archiveCountSource{archiveErrorSource: archiveErrorSource{err: tc.err}, count: tc.count}
			jobs := archivesvc.NewMemoryStore()
			if tc.prepare != nil {
				tc.prepare(jobs)
			}
			h := NewArchiveHandler(archivesvc.New(jobs, source, nil, archivesvc.Options{}), archiveTestAuth(tc.authorized))
			rec := serveArchive(h.Create(observabilityvo.ArchiveKindLog), http.MethodPost, "/archives")
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.code) {
				t.Fatalf("unexpected response %d %s", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "signature=secret") || strings.Contains(rec.Body.String(), "private.test") {
				t.Fatal("internal error leaked")
			}
			if source.purges != 0 {
				t.Fatal("submission must not purge")
			}
			if !tc.authorized && source.freezes != 0 {
				t.Fatal("unauthorized request queried archive source")
			}
		})
	}
}

func TestArchiveSubmitQueuesRunAndExposesProgressAndRetained(t *testing.T) {
	jobs := archivesvc.NewMemoryStore()
	source := &archiveCountSource{count: 3}
	service := archivesvc.New(jobs, archivesvc.Router{Trace: source}, nil, archivesvc.Options{})
	h := NewArchiveHandler(service, archiveTestAuth(true))
	rec := serveArchive(h.Create(observabilityvo.ArchiveKindTrace), http.MethodPost, "/archives")
	var run struct {
		RunID           string `json:"run_id"`
		Status          string `json:"status"`
		EligibleAtStart int    `json:"eligible_at_start"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &run); err != nil || rec.Code != http.StatusAccepted || run.RunID == "" || run.Status != "queued" || run.EligibleAtStart != 3 {
		t.Fatalf("submit: %d %s", rec.Code, rec.Body.String())
	}
	if source.purges != 0 {
		t.Fatal("submission must return before any batch runs")
	}
	retained := archivesvc.RetainedInteraction{InteractionID: "large", RunID: run.RunID, PolicyVersion: archivesvc.TraceArchivePolicyVersion(), Stage: "sql_payload", Measured: 12 << 20, Budget: 8 << 20, Unit: "bytes", RetainedAt: time.Now()}
	if err := jobs.(archivesvc.RunStore).RetainInteraction(context.Background(), retained); err != nil {
		t.Fatal(err)
	}
	rec = serveArchive(h.RunResult, http.MethodGet, "/api/observability/v1/archive-runs/"+run.RunID)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"interaction_id"`) || !strings.Contains(rec.Body.String(), `"retained_count":1`) {
		t.Fatalf("run result: %d %s", rec.Code, rec.Body.String())
	}
	rec = serveArchive(h.RunResult, http.MethodGet, "/api/observability/v1/archive-runs/"+run.RunID+"/retained?offset=0&limit=20")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"interaction_id":"large"`) || !strings.Contains(rec.Body.String(), `"total":1`) {
		t.Fatalf("run retained page: %d %s", rec.Code, rec.Body.String())
	}
	rec = serveArchive(h.Retained, http.MethodGet, "/api/observability/v1/trace-archive-retained?limit=20")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"budget":8388608`) || !strings.Contains(rec.Body.String(), `"total":1`) {
		t.Fatalf("retained list: %d %s", rec.Code, rec.Body.String())
	}
	for _, bad := range []string{"?limit=101", "?offset=-1", "?limit=x"} {
		if rec = serveArchive(h.Retained, http.MethodGet, "/api/observability/v1/trace-archive-retained"+bad); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s accepted: %d", bad, rec.Code)
		}
	}
	if rec = serveArchive(h.RunResult, http.MethodGet, "/api/observability/v1/archive-runs/missing/retained"); rec.Code != http.StatusNotFound {
		t.Fatalf("missing run retained page: %d", rec.Code)
	}
	rec = serveArchive(h.Overview(observabilityvo.ArchiveKindTrace), http.MethodGet, "/overview")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"retained_count":1`) || !strings.Contains(rec.Body.String(), `"run_id":"`+run.RunID+`"`) {
		t.Fatalf("overview: %d %s", rec.Code, rec.Body.String())
	}
	rec = serveArchive(h.RunResult, http.MethodGet, "/api/observability/v1/archive-runs/missing")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing run: %d", rec.Code)
	}
	if rec := serveArchive(NewArchiveHandler(service, archiveTestAuth(false)).RunResult, http.MethodGet, "/api/observability/v1/archive-runs/"+run.RunID); rec.Code != http.StatusForbidden {
		t.Fatalf("unauthorized run result: %d", rec.Code)
	}
}

type failedArchiveHistoryStore struct {
	archivesvc.Store
	calls int
}

func (store *failedArchiveHistoryStore) ListJobs(context.Context, observabilityvo.ArchiveKind, int) ([]archivesvc.Job, error) {
	store.calls++
	return nil, fmt.Errorf("database password and private URL must not escape")
}

func TestArchiveHistoryFailureIsUnavailableAndAuthorized(t *testing.T) {
	for _, allowed := range []bool{true, false} {
		roles := []string{"user"}
		if allowed {
			roles = []string{"admin"}
		}
		auth := NewEvidenceHandlerWithSecurityConfig(nil, EvidenceHandlerSecurityConfig{
			HydraAdminURL: "http://hydra.test", QueryHTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"active":true,"sub":"user-1","client_id":"cli","ext":{"visitor_type":"user"}}`))}, nil
			})},
			AuthorizationScopeResolver: &fakeAccessScopeResolver{profile: evidencevo.AccessProfile{ActorID: "user-1", EffectiveSubjectID: "user-1", AccountActive: true, Roles: roles}},
		})
		store := &failedArchiveHistoryStore{Store: archivesvc.NewMemoryStore()}
		handler := NewArchiveHandler(archivesvc.New(store, nil, nil, archivesvc.Options{}), auth)
		for _, kind := range []observabilityvo.ArchiveKind{observabilityvo.ArchiveKindLog, observabilityvo.ArchiveKindTrace} {
			req := httptest.NewRequest(http.MethodGet, "/archives", nil)
			req.Header.Set("Authorization", "Bearer test-token")
			rec := httptest.NewRecorder()
			handler.ListOrCreate(kind)(rec, req)
			want := http.StatusForbidden
			if allowed {
				want = http.StatusServiceUnavailable
			}
			if rec.Code != want || strings.Contains(rec.Body.String(), "password") {
				t.Fatalf("%d %s", rec.Code, rec.Body.String())
			}
			if allowed && !strings.Contains(rec.Body.String(), "archive_history_unavailable") {
				t.Fatal(rec.Body.String())
			}
		}
		if !allowed && store.calls != 0 {
			t.Fatal("unauthorized history query")
		}
	}
}
