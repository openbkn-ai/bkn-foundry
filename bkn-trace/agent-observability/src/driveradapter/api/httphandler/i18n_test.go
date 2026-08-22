// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	observabilitylocale "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/locale"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/evidencesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/sessionsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/tracesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/driveradapter/api/rdto"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

type failingTechnicalTraceSummarySource struct{}

func (failingTechnicalTraceSummarySource) ListTraceExecutions(
	context.Context,
	evidencevo.SummaryQueryOptions,
) (evidencevo.TraceSummaryPage, error) {
	return evidencevo.TraceSummaryPage{}, errors.New("summary backend unavailable")
}

func TestLocalizedParameterMissingErrorsPreserveMachineContract(t *testing.T) {
	handler := NewTraceHandler(nil)
	serve := localizedTestHandler(http.HandlerFunc(handler.GetTechnicalTraceDetail))

	assertFlatLocalizedError(t, serve, http.MethodGet,
		"/api/agent-observability/v1/traces/", nil,
		http.StatusBadRequest, "INVALID_ARGUMENT",
		"trace_id is required", "缺少 trace_id 参数",
	)
}

func TestLocalizedAuthenticationRejectionPreservesMachineContractAndPropagatesLocale(t *testing.T) {
	var downstreamLanguage string
	hydra := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downstreamLanguage = r.Header.Get(sharedrest.AcceptLanguageHeader)
		_, _ = io.WriteString(w, `{"active":false}`)
	}))
	defer hydra.Close()

	handler := NewEvidenceHandlerWithSecurityConfig(
		evidencesvc.New(evidencestore.New()),
		EvidenceHandlerSecurityConfig{HydraAdminURL: hydra.URL},
	)
	serve := localizedTestHandler(http.HandlerFunc(handler.GetEvidenceChainByTraceID))
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/agent-observability/v1/traces/missing/evidence-chain",
		nil,
	)
	request.Header.Set("Authorization", "Bearer inactive-token")
	request.Header.Set("x-business-domain", "bd-demo")
	request.Header.Set(sharedrest.AcceptLanguageHeader, "en-GB,en;q=0.9,zh-CN;q=0.8")
	response := httptest.NewRecorder()

	serve.ServeHTTP(response, request)

	var body rdto.ErrorResponse
	decodeLocalizedResponse(t, response, &body)
	if response.Code != http.StatusUnauthorized || body.ErrorCode != "QUERY_OAUTH_REQUIRED" ||
		body.Message != "active OAuth bearer token is required" {
		t.Fatalf("unexpected auth rejection: status=%d body=%+v", response.Code, body)
	}
	assertLocalizedHeaders(t, response, sharedrest.AmericanEnglish)
	if downstreamLanguage != sharedrest.AmericanEnglish {
		t.Fatalf("downstream Accept-Language = %q, want %q", downstreamLanguage, sharedrest.AmericanEnglish)
	}
}

func TestLocalizedTraceNotFoundPreservesMachineContract(t *testing.T) {
	handler := NewTraceHandlerWithTechnicalSources(
		tracesvc.New(&fakeTraceHandlerPort{}),
		fakeTechnicalTraceSummarySource{},
		fakeTechnicalOperationSource{},
	)
	serve := localizedTestHandler(http.HandlerFunc(handler.GetTechnicalTraceDetail))

	assertFlatLocalizedError(t, serve, http.MethodGet,
		"/api/agent-observability/v1/traces/missing", trustedTraceQueryContext,
		http.StatusNotFound, "NOT_FOUND",
		"trace not found", "Trace 不存在",
	)
}

func TestLocalizedDownstreamQueryFailurePreservesMachineContract(t *testing.T) {
	handler := NewTraceHandlerWithTechnicalSources(
		tracesvc.New(&fakeTraceHandlerPort{}),
		failingTechnicalTraceSummarySource{},
		fakeTechnicalOperationSource{},
	)
	serve := localizedTestHandler(http.HandlerFunc(handler.GetTechnicalTraceDetail))

	assertFlatLocalizedError(t, serve, http.MethodGet,
		"/api/agent-observability/v1/traces/query-failure", trustedTraceQueryContext,
		http.StatusInternalServerError, "QUERY_FAILED",
		"failed to query trace summary", "Trace 摘要查询失败",
	)
}

func TestLocalizedConversationRequiredPreservesLifecycleGuidance(t *testing.T) {
	handler := NewSessionHandler(sessionsvc.New(sessionstore.New(), sessionsvc.Options{}))
	serve := localizedTestHandler(http.HandlerFunc(handler.ResumeConversation))

	type responseEnvelope struct {
		Error lifecycleError `json:"error"`
	}
	var baseline *responseEnvelope
	for _, test := range []struct {
		language string
		message  string
	}{
		{language: sharedrest.AmericanEnglish, message: "conversation_id is required"},
		{language: sharedrest.SimplifiedChinese, message: "缺少 conversation_id 参数"},
	} {
		t.Run(test.language, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost,
				"/api/agent-observability/v1/conversations:resume-by-id",
				bytes.NewBufferString(`{}`),
			)
			setI18nTrustedOwnerHeaders(request)
			request.Header.Set(sharedrest.AcceptLanguageHeader, test.language)
			response := httptest.NewRecorder()

			serve.ServeHTTP(response, request)

			var body responseEnvelope
			decodeLocalizedResponse(t, response, &body)
			if response.Code != http.StatusBadRequest || body.Error.Code != "conversation_required" ||
				body.Error.Message != test.message || body.Error.Retryable ||
				body.Error.RequiredAction != "bkn_start_interaction" {
				t.Fatalf("unexpected lifecycle error: status=%d body=%+v", response.Code, body.Error)
			}
			assertLocalizedHeaders(t, response, test.language)
			if baseline == nil {
				baseline = &body
				return
			}
			if body.Error.Code != baseline.Error.Code ||
				body.Error.Retryable != baseline.Error.Retryable ||
				body.Error.RequiredAction != baseline.Error.RequiredAction {
				t.Fatalf("localized lifecycle machine fields changed: baseline=%+v actual=%+v", baseline.Error, body.Error)
			}
		})
	}
}

func TestLocalizedObservabilityErrorPreservesMachineContract(t *testing.T) {
	serve := localizedTestHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeObservabilityError(
			w,
			r,
			http.StatusNotFound,
			"log_not_disclosed",
			"log was not found in the authorized scope",
		)
	}))

	type responseEnvelope struct {
		Error observabilityError `json:"error"`
	}
	var baseline *responseEnvelope
	for _, test := range []struct {
		language string
		message  string
	}{
		{language: sharedrest.AmericanEnglish, message: "log was not found in the authorized scope"},
		{language: sharedrest.SimplifiedChinese, message: "授权范围内不存在该日志"},
	} {
		t.Run(test.language, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/observability/v1/logs/log-1", nil)
			request.Header.Set(sharedrest.AcceptLanguageHeader, test.language)
			response := httptest.NewRecorder()

			serve.ServeHTTP(response, request)

			var body responseEnvelope
			decodeLocalizedResponse(t, response, &body)
			if response.Code != http.StatusNotFound || body.Error.Code != "log_not_disclosed" ||
				body.Error.Message != test.message || body.Error.Retryable {
				t.Fatalf("unexpected observability error: status=%d body=%+v", response.Code, body.Error)
			}
			assertLocalizedHeaders(t, response, test.language)
			if baseline == nil {
				baseline = &body
				return
			}
			if body.Error.Code != baseline.Error.Code || body.Error.Retryable != baseline.Error.Retryable {
				t.Fatalf("localized observability machine fields changed: baseline=%+v actual=%+v", baseline.Error, body.Error)
			}
		})
	}
}

func TestLocalizedArtifactRoleMismatchPreservesDynamicValidationDetails(t *testing.T) {
	store := evidencestore.New()
	service := evidencesvc.NewWithArtifactStore(store, store)
	if _, validationErrors, err := service.Ingest(
		context.Background(),
		[]byte(validHandlerArtifactEventBatch()),
	); err != nil || len(validationErrors) > 0 {
		t.Fatalf("seed artifact-linked trace: validation=%+v err=%v", validationErrors, err)
	}
	handler := NewEvidenceHandlerWithSecurityConfig(
		service,
		EvidenceHandlerSecurityConfig{AllowUnauthenticatedIngest: true},
	)
	serve := localizedTestHandler(http.HandlerFunc(handler.IngestEvidenceArtifact))
	body := strings.Replace(validHandlerArtifact(), `"artifact_type": "question"`, `"artifact_type": "result"`, 1)

	type responseEnvelope struct {
		ErrorCode string                      `json:"error_code"`
		Code      string                      `json:"code"`
		Message   string                      `json:"message"`
		Details   evidencevo.ValidationErrors `json:"details"`
	}
	for _, test := range []struct {
		language string
		message  string
	}{
		{
			language: sharedrest.AmericanEnglish,
			message:  "artifact_type does not match event link role agent.interaction.started.question_artifact_ref",
		},
		{
			language: sharedrest.SimplifiedChinese,
			message:  "artifact_type 与事件关联角色 agent.interaction.started.question_artifact_ref 不匹配",
		},
	} {
		t.Run(test.language, func(t *testing.T) {
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/agent-observability/v1/evidence/artifacts",
				strings.NewReader(body),
			)
			request.Header.Set(sharedrest.AcceptLanguageHeader, test.language)
			response := httptest.NewRecorder()

			serve.ServeHTTP(response, request)

			var result responseEnvelope
			decodeLocalizedResponse(t, response, &result)
			if response.Code != http.StatusBadRequest ||
				result.ErrorCode != "BKN_TRACE_ARTIFACT_TYPE_MISMATCH" ||
				result.Code != result.ErrorCode || result.Message != test.message ||
				len(result.Details) != 1 || result.Details[0].Code != result.ErrorCode ||
				result.Details[0].Path != "$.artifact_type" || result.Details[0].Message != test.message {
				t.Fatalf("unexpected artifact role validation response: status=%d body=%+v", response.Code, result)
			}
			assertLocalizedHeaders(t, response, test.language)
		})
	}
}

func TestSuccessfulMachineResponseDoesNotDeclareContentLanguage(t *testing.T) {
	serve := localizedTestHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, r, http.StatusOK, map[string]string{"status": "completed"})
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/success", nil)
	request.Header.Set(sharedrest.AcceptLanguageHeader, sharedrest.SimplifiedChinese)
	response := httptest.NewRecorder()

	serve.ServeHTTP(response, request)

	if got := response.Header().Get(sharedrest.ContentLanguageHeader); got != "" {
		t.Fatalf("successful machine response Content-Language = %q, want empty", got)
	}
	if got := response.Header().Get("Cache-Control"); got != "private, no-cache" {
		t.Fatalf("successful authenticated response Cache-Control = %q", got)
	}
}

func localizedTestHandler(handler http.Handler) http.Handler {
	return observabilitylocale.PrivateNoCacheForPrefixes(
		observabilitylocale.LanguageMiddleware(handler),
		"/api/agent-observability/v1",
		"/api/observability/v1",
	)
}

func trustedTraceQueryContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, trustedQueryScopeContextKey{}, evidencevo.QueryScope{
		TenantID: "tenant-1", BusinessDomain: "domain-1", AccountID: "user-1", AccountType: "user",
	})
}

func setI18nTrustedOwnerHeaders(request *http.Request) {
	request.Header.Set("X-BKN-Tenant-ID", "tenant-1")
	request.Header.Set("X-Business-Domain-ID", "domain-1")
	request.Header.Set("X-BKN-Application-Principal-ID", "app-1")
	request.Header.Set("X-BKN-Effective-Subject-Type", "user")
	request.Header.Set("X-BKN-Effective-Subject-ID", "user-1")
}

func assertFlatLocalizedError(
	t *testing.T,
	handler http.Handler,
	method string,
	path string,
	decorateContext func(context.Context) context.Context,
	status int,
	code string,
	englishMessage string,
	chineseMessage string,
) {
	t.Helper()
	var baseline *rdto.ErrorResponse
	for _, test := range []struct {
		language string
		message  string
	}{
		{language: sharedrest.AmericanEnglish, message: englishMessage},
		{language: sharedrest.SimplifiedChinese, message: chineseMessage},
	} {
		t.Run(test.language, func(t *testing.T) {
			request := httptest.NewRequest(method, path, nil)
			if decorateContext != nil {
				request = request.WithContext(decorateContext(request.Context()))
			}
			request.Header.Set(sharedrest.AcceptLanguageHeader, test.language)
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			var body rdto.ErrorResponse
			decodeLocalizedResponse(t, response, &body)
			if response.Code != status || body.ErrorCode != code || body.Code != code || body.Message != test.message {
				t.Fatalf("unexpected localized error: status=%d body=%+v", response.Code, body)
			}
			assertLocalizedHeaders(t, response, test.language)
			if baseline == nil {
				baseline = &body
				return
			}
			if body.ErrorCode != baseline.ErrorCode || body.Code != baseline.Code {
				t.Fatalf("localized machine fields changed: baseline=%+v actual=%+v", baseline, body)
			}
		})
	}
}

func decodeLocalizedResponse(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response: %v: %s", err, response.Body.String())
	}
}

func assertLocalizedHeaders(t *testing.T, response *httptest.ResponseRecorder, language string) {
	t.Helper()
	if got := response.Header().Get(sharedrest.ContentLanguageHeader); got != language {
		t.Fatalf("Content-Language = %q, want %q", got, language)
	}
	if got := response.Header().Get("Cache-Control"); got != "private, no-cache" {
		t.Fatalf("Cache-Control = %q, want private, no-cache", got)
	}
	if got := response.Header().Get("Vary"); got != sharedrest.AcceptLanguageHeader {
		t.Fatalf("Vary = %q, want %q", got, sharedrest.AcceptLanguageHeader)
	}
}
