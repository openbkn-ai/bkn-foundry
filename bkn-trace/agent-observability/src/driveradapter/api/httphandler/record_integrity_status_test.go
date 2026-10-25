// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/evidencesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	memorystore "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

const retryStatusPath = "/api/agent-observability/v1/interactions/int/record-integrity-status"

type retryStatusReadStore struct {
	*memorystore.Store
	reads, claims, finishes, snapshots int
	version                            uint64
}

func (s *retryStatusReadStore) GetRecordIntegrityRetry(ctx context.Context, id string, version uint64) (isessionstore.RecordIntegrityRetry, bool, error) {
	s.reads++
	return s.Store.GetRecordIntegrityRetry(ctx, id, version)
}
func (s *retryStatusReadStore) ClaimRecordIntegrityRetry(ctx context.Context, id string, version uint64) (isessionstore.RecordIntegrityRetry, bool, error) {
	s.claims++
	return s.Store.ClaimRecordIntegrityRetry(ctx, id, version)
}
func (s *retryStatusReadStore) FinishRecordIntegrityRetry(ctx context.Context, claim isessionstore.RecordIntegrityRetry, stored *sessionvo.StoredRecordIntegrity, reason string) (bool, error) {
	s.finishes++
	return s.Store.FinishRecordIntegrityRetry(ctx, claim, stored, reason)
}
func (s *retryStatusReadStore) ReadEvidenceSnapshot(context.Context, string) (sessionvo.EvidenceSnapshot, bool, error) {
	s.snapshots++
	return sessionvo.EvidenceSnapshot{}, false, errors.New("diagnostic read must not inspect live evidence")
}

func retryStatusFixture(t *testing.T, status sessionvo.InteractionStatus) (*retryStatusReadStore, *evidencesvc.Service) {
	t.Helper()
	store := &retryStatusReadStore{Store: memorystore.New()}
	err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		tx.SaveConversation(sessionvo.Conversation{ID: "conv", Owner: sessionvo.Owner{ApplicationPrincipalID: "app", EffectiveSubjectType: sessionvo.SubjectUser, EffectiveSubjectID: "owner"}})
		tx.SaveInteraction(sessionvo.Interaction{ID: "int", ConversationID: "conv", ExecutionStatus: status})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		interaction, _ := tx.PeekInteraction("int")
		store.version = interaction.IntegritySourceVersion
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return store, evidencesvc.New(evidencestore.New(), evidencesvc.WithSessionStore(store), evidencesvc.WithCurrentRecordIntegrity())
}
func retryStatusMux(handler *EvidenceHandler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/agent-observability/v1/interactions/{interaction_id}/record-integrity-status", handler.RequireTrustedQueryIdentity(handler.GetRecordIntegrityRetryStatus))
	return mux
}
func retryStatusRequest(account, target string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	if account != "" {
		request.Header.Set("x-account-id", account)
		request.Header.Set("x-account-type", "user")
	}
	return request
}

func TestRecordIntegrityStatusAuthorizesBeforeReadingRetryRegistry(t *testing.T) {
	for _, tc := range []struct {
		name, account, target string
		profile               *evidencevo.AccessProfile
		want, reads           int
	}{
		{name: "owner", account: "owner", target: retryStatusPath, want: 200, reads: 1},
		{name: "cross owner", account: "other", target: retryStatusPath, want: 404},
		{name: "missing", account: "owner", target: strings.Replace(retryStatusPath, "/int/", "/missing/", 1), want: 404},
		{name: "no identity", target: retryStatusPath, want: 401},
		{name: "query identity cannot replace identity", target: retryStatusPath + "?owner=owner&account_id=owner", want: 401},
		{name: "query owner rejected", account: "other", target: retryStatusPath + "?owner=owner", want: 400},
		{name: "shared network cannot widen canonical verdict scope", account: "other", target: retryStatusPath, profile: &evidencevo.AccessProfile{EffectiveSubjectID: "other", AccountActive: true, Roles: []string{"network_builder"}, ManagedKnowledgeNetworkIDs: []string{"shared"}}, want: 404},
		{name: "inactive owner", account: "owner", target: retryStatusPath, profile: &evidencevo.AccessProfile{EffectiveSubjectID: "owner", AccountActive: false}, want: 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, service := retryStatusFixture(t, sessionvo.InteractionCompleted)
			config := EvidenceHandlerSecurityConfig{AllowUnauthenticatedQuery: true}
			if tc.profile != nil {
				config.AuthorizationScopeResolver = &fakeAccessScopeResolver{profile: *tc.profile}
			}
			response := httptest.NewRecorder()
			retryStatusMux(NewEvidenceHandlerWithSecurityConfig(service, config)).ServeHTTP(response, retryStatusRequest(tc.account, tc.target))
			if response.Code != tc.want || store.reads != tc.reads {
				t.Fatalf("status=%d reads=%d: %s", response.Code, store.reads, response.Body.String())
			}
			if store.claims != 0 || store.finishes != 0 || store.snapshots != 0 {
				t.Fatalf("diagnostics performed work: %+v", store)
			}
		})
	}
}

func TestRecordIntegrityStatusReadDoesNotClaimInspectOrExposeToken(t *testing.T) {
	store, service := retryStatusFixture(t, sessionvo.InteractionCompleted)
	claim, found, err := store.Store.ClaimRecordIntegrityRetry(context.Background(), "int", store.version)
	if err != nil || !found || claim.Token == "" {
		t.Fatalf("seed claim: %+v %v", claim, err)
	}
	mux := retryStatusMux(newDevEvidenceHandler(service))
	for i := 0; i < 2; i++ {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, retryStatusRequest("owner", retryStatusPath))
		if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%d %s", response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), claim.Token) || strings.Contains(response.Body.String(), "token") {
			t.Fatal("worker token leaked")
		}
		var status isessionstore.RecordIntegrityRetry
		if err = json.Unmarshal(response.Body.Bytes(), &status); err != nil {
			t.Fatal(err)
		}
		if status.InteractionID != "int" || status.SourceVersion != store.version || status.Status != isessionstore.RecordIntegrityRunning || status.Attempts != 1 || status.LastAttemptAt.IsZero() || status.LeaseUntil.IsZero() {
			t.Fatalf("lost diagnostic metadata: %+v", status)
		}
	}
	after, found, err := store.Store.GetRecordIntegrityRetry(context.Background(), "int", store.version)
	if err != nil || !found || !reflect.DeepEqual(claim, after) {
		t.Fatal("read changed retry registry")
	}
	if saved, err := store.Store.FinishRecordIntegrityRetry(context.Background(), claim, nil, "inspection_failed"); err != nil || !saved {
		t.Fatalf("seed failure: %v", err)
	}
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, retryStatusRequest("owner", retryStatusPath))
	var failed isessionstore.RecordIntegrityRetry
	if err = json.Unmarshal(response.Body.Bytes(), &failed); err != nil || response.Code != 200 || failed.Status != isessionstore.RecordIntegrityRetryWait || failed.Reason != "inspection_failed" || failed.LastFailureReason != "inspection_failed" {
		t.Fatalf("lost failure diagnostics: %s %v", response.Body.String(), err)
	}
	if store.reads != 3 || store.claims != 0 || store.finishes != 0 || store.snapshots != 0 {
		t.Fatalf("diagnostics performed work: %+v", store)
	}
}

func TestRecordIntegrityStatusSynthesizesWithoutWritingRegistry(t *testing.T) {
	for _, tc := range []struct {
		name      string
		execution sessionvo.InteractionStatus
		stored    bool
		want      string
	}{
		{"pending", sessionvo.InteractionCompleted, false, "pending"},
		{"active", sessionvo.InteractionActive, false, "active"},
		{"saved verdict", sessionvo.InteractionCompleted, true, isessionstore.RecordIntegritySucceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, service := retryStatusFixture(t, tc.execution)
			if tc.stored {
				saved, err := store.SaveStoredRecordIntegrity(context.Background(), "int", sessionvo.StoredRecordIntegrity{SourceVersion: store.version, Applicable: true, Report: &sessionvo.RecordIntegrity{Status: "missing", Scope: "registered_call_records", CheckedAt: time.Now().UTC(), Missing: []sessionvo.MissingRecord{{OperationID: "op", Attempt: 1, Reason: "missing", Field: "output"}}}})
				if err != nil || !saved {
					t.Fatalf("seed verdict: %v", err)
				}
			}
			response := httptest.NewRecorder()
			retryStatusMux(newDevEvidenceHandler(service)).ServeHTTP(response, retryStatusRequest("owner", retryStatusPath))
			var status isessionstore.RecordIntegrityRetry
			if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil || response.Code != 200 || status.Status != tc.want || status.Attempts != 0 {
				t.Fatalf("%d %s err=%v", response.Code, response.Body.String(), err)
			}
			if _, exists, err := store.Store.GetRecordIntegrityRetry(context.Background(), "int", store.version); err != nil || exists {
				t.Fatalf("read created registry row: %v", err)
			}
			if store.claims != 0 || store.finishes != 0 || store.snapshots != 0 {
				t.Fatalf("diagnostics performed work: %+v", store)
			}
		})
	}
}
