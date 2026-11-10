// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logics

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	oerrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

type countingResolver struct{ calls int }

func (r *countingResolver) Resolve(_ context.Context,
	binding interfaces.TrustedProxyBinding) (*interfaces.TrustedProxyContext, error) {
	r.calls++
	return &interfaces.TrustedProxyContext{PublishedModelVersion: "proxy-model-9", Binding: binding}, nil
}

func pinned(t *testing.T, knVersion, draftRevision, observed, observedRevision string) context.Context {
	t.Helper()
	pin, err := interfaces.NewKNVersionPin(knVersion, draftRevision)
	if err != nil {
		t.Fatal(err)
	}
	if err := pin.Observe(observed, observedRevision); err != nil {
		t.Fatal(err)
	}
	return interfaces.WithKNVersionPin(context.Background(), pin)
}

func TestResolveDataProxyUsesTheProxyOnlyForTheRunningVersion(t *testing.T) {
	for name, test := range map[string]struct {
		ctx       context.Context
		wantProxy bool
	}{
		"no pin":                  {context.Background(), true},
		"unversioned network":     {pinned(t, "", "", "", ""), true},
		"running, no kn_version":  {pinned(t, "", "", "3", ""), true},
		"running, kn_version set": {pinned(t, "running", "", "3", ""), true},
		"history version":         {pinned(t, "2", "", "2", ""), false},
		"draft":                   {pinned(t, "draft", "", "draft", "7"), false},
	} {
		t.Run(name, func(t *testing.T) {
			resolver := &countingResolver{}
			got, err := ResolveDataProxy(test.ctx, resolver, interfaces.TrustedProxyBinding{KNID: "kn-1"})
			if err != nil {
				t.Fatal(err)
			}
			if (got != nil) != test.wantProxy || (resolver.calls == 1) != test.wantProxy {
				t.Fatalf("proxy context = %v after %d resolutions, want proxy=%v", got, resolver.calls, test.wantProxy)
			}
		})
	}
}

func TestReadVersionTokenNamesTheVersionRead(t *testing.T) {
	proxy := &interfaces.TrustedProxyContext{PublishedModelVersion: "proxy-model-9"}
	for name, test := range map[string]struct {
		ctx   context.Context
		proxy *interfaces.TrustedProxyContext
		want  string
	}{
		"unversioned keeps the proxy model version": {pinned(t, "", "", "", ""), proxy, "proxy-model-9"},
		"no pin keeps the proxy model version":      {context.Background(), proxy, "proxy-model-9"},
		"running version":                           {pinned(t, "", "", "3", ""), proxy, "v3"},
		"history version":                           {pinned(t, "2", "", "2", ""), nil, "v2"},
		"draft revision":                            {pinned(t, "draft", "", "draft", "7"), nil, "draft@7"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := ReadVersionToken(test.ctx, test.proxy); got != test.want {
				t.Fatalf("ReadVersionToken() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestKNVersionErrorPassesVersionRefusalsThrough(t *testing.T) {
	ctx := context.Background()
	for _, code := range []string{
		oerrors.BknBackend_KNVersion_VersionPurged,
		oerrors.BknBackend_KNVersion_DraftRevisionMismatch,
		oerrors.BknBackend_KNVersion_VersionNotFound,
	} {
		wrapped := errors.Join(errors.New("get object type failed"),
			httperrors.NewHTTPError(ctx, http.StatusGone, code))
		got, ok := KNVersionError(ctx, wrapped)
		if !ok || got.ErrorCode != code || got.HTTPCode != http.StatusGone {
			t.Errorf("KNVersionError(%s) = %#v, %v", code, got, ok)
		}
	}
	for _, err := range []error{
		errors.New("connection refused"),
		httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden),
	} {
		if got, ok := KNVersionError(ctx, err); ok {
			t.Errorf("KNVersionError(%v) = %#v, want no pass-through", err, got)
		}
	}
}
