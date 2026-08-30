// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package enterpriseroute

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionoutbox"
)

func TestHistoricalProvenanceHandlerIsAvailableOnlyWhenRegistered(t *testing.T) {
	ResetForTest()
	t.Cleanup(ResetForTest)
	if HistoricalProvenanceHandler() != nil {
		t.Fatal("unexpected handler before registration")
	}
	handler := projectionHandlerFunc(func(context.Context, iprojectionoutbox.Item) error { return nil })
	RegisterHistoricalProvenanceHandler(handler)
	if HistoricalProvenanceHandler() == nil {
		t.Fatal("registered handler is unavailable")
	}
}

type projectionHandlerFunc func(context.Context, iprojectionoutbox.Item) error

func (f projectionHandlerFunc) HandleHistoricalProvenance(ctx context.Context, item iprojectionoutbox.Item) error {
	return f(ctx, item)
}

func TestCommunityBuildHasNoEnterpriseRoutes(t *testing.T) {
	ResetForTest()
	t.Cleanup(ResetForTest)

	mux := http.NewServeMux()
	Mount(mux, fakeReader{}, nil)

	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/business-provenance/interactions/int-1", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("community business provenance route = %d, want 404", response.Code)
	}
}

func TestRegisterAfterMountPanics(t *testing.T) {
	ResetForTest()
	t.Cleanup(ResetForTest)

	Mount(http.NewServeMux(), fakeReader{}, nil)
	defer func() {
		if recover() == nil {
			t.Fatal("Register after Mount must panic")
		}
	}()
	Register(func(Registrar, Reader) {})
}

func TestMounterReceivesCoreReader(t *testing.T) {
	ResetForTest()
	t.Cleanup(ResetForTest)

	called := false
	Register(func(_ Registrar, reader Reader) {
		called = reader != nil
	})
	Mount(http.NewServeMux(), fakeReader{}, func(next http.Handler) http.Handler { return next })
	if !called {
		t.Fatal("enterprise mounter did not receive the Core fact reader")
	}
}

type fakeReader struct{}

func (fakeReader) ReadInteraction(context.Context, string) (InteractionFacts, bool, error) {
	return InteractionFacts{Summary: evidencevo.InteractionSummary{}}, false, nil
}

func (fakeReader) ListConversations(context.Context, ListQuery) (evidencevo.ConversationSummaryPage, error) {
	return evidencevo.ConversationSummaryPage{}, nil
}

func (fakeReader) ListInteractions(context.Context, ListQuery) (evidencevo.InteractionSummaryPage, error) {
	return evidencevo.InteractionSummaryPage{}, nil
}

func TestAvailabilityGateRunsBeforeCoreAuthorization(t *testing.T) {
	ResetForTest()
	t.Cleanup(ResetForTest)
	Register(func(routes Registrar, _ Reader) {
		routes.Handle("/enterprise-probe", func(_ http.Handler) http.Handler {
			return http.NotFoundHandler()
		}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
	})
	mux := http.NewServeMux()
	Mount(mux, fakeReader{}, func(_ http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		})
	})

	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/enterprise-probe", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("unlicensed enterprise route = %d, want 404 before Core authorization", response.Code)
	}
}
