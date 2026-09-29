// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package permissionguide

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

const testTemplate = "/studio/knowledge-network/workspace/{kn_id}/object-types/{ot_id}/detail?requestPermission={scope_code}"

type schemaProbeStub struct {
	err   error
	calls int
}

func (s *schemaProbeStub) GetObjectTypeSchema(context.Context, string, string) (*interfaces.ObjectTypeSchemaResp, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return &interfaces.ObjectTypeSchemaResp{}, nil
}

type objectTypeReaderStub struct{ name string }

func (s objectTypeReaderStub) GetObjectTypeDetail(_ context.Context, _ string, otIDs []string, _ bool) ([]*interfaces.ObjectType, error) {
	return []*interfaces.ObjectType{{ID: otIDs[0], Name: s.name}}, nil
}

func forbidden() error {
	return &infraErr.HTTPError{HTTPCode: http.StatusForbidden, Code: "Public.Forbidden"}
}

func userCtx(origin string) context.Context {
	ctx := common.SetAccountAuthContextToCtx(context.Background(), &interfaces.AccountAuthContext{
		AccountID: "u-1", AccountType: interfaces.AccessorTypeUser,
	})
	return common.SetPublicOriginToCtx(ctx, origin)
}

func newTestGuide(probe *schemaProbeStub) *Guide {
	return New(config.PermissionRequestConfig{PathTemplate: testTemplate}, probe, objectTypeReaderStub{name: "客户信息"})
}

func TestForbiddenQueryGetsGrantLinkOnTheCallersHost(t *testing.T) {
	guide := newTestGuide(&schemaProbeStub{})

	got := guide.ForObjectTypeError(userCtx("https://bkn.example.com"), forbidden(), "55555", "6666")

	want := &interfaces.PermissionGuidance{
		Resource: interfaces.PermissionGuidanceResource{
			Type: "object_type", ID: "55555/6666", KnID: "55555", OtID: "6666", Name: "客户信息",
		},
		Shortfalls: []interfaces.PermissionShortfall{{
			Scope: interfaces.PermissionScopeGrant, Operations: []string{"query_data"},
			RequestPermissionURL: "https://bkn.example.com/studio/knowledge-network/workspace/55555/object-types/6666/detail?requestPermission=1",
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("guidance = %+v\nwant %+v", got, want)
	}
}

func TestInClusterCallerGetsARelativeLink(t *testing.T) {
	guide := newTestGuide(&schemaProbeStub{})

	got := guide.ForObjectTypeError(userCtx(""), forbidden(), "kn", "ot")

	if link := got.Shortfalls[0].RequestPermissionURL; link !=
		"/studio/knowledge-network/workspace/kn/object-types/ot/detail?requestPermission=1" {
		t.Fatalf("link = %q, want the path on the Studio host", link)
	}
}

func TestCallerWhoCannotViewTheObjectTypeGetsNoLinkAndNoName(t *testing.T) {
	guide := newTestGuide(&schemaProbeStub{err: forbidden()})

	got := guide.ForObjectTypeError(userCtx("https://bkn.example.com"), forbidden(), "kn", "ot")

	if got == nil || got.Resource.Name != "" {
		t.Fatalf("guidance = %+v, want the refusal without a name", got)
	}
	if s := got.Shortfalls[0]; s.RequestPermissionURL != "" {
		t.Fatalf("shortfall = %+v, want no link", s)
	}
}

func TestNoLinkWhenRequestsCannotHelp(t *testing.T) {
	appCtx := common.SetAccountAuthContextToCtx(context.Background(), &interfaces.AccountAuthContext{
		AccountID: "app-1", AccountType: interfaces.AccessorTypeApp,
	})
	cases := map[string]struct {
		guide *Guide
		ctx   context.Context
	}{
		"disabled deployment": {
			guide: New(config.PermissionRequestConfig{Disabled: true, PathTemplate: testTemplate}, &schemaProbeStub{}, nil),
			ctx:   userCtx("https://bkn.example.com"),
		},
		"application account": {guide: newTestGuide(&schemaProbeStub{}), ctx: appCtx},
		"no caller":           {guide: newTestGuide(&schemaProbeStub{}), ctx: context.Background()},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := tc.guide.ForObjectTypeError(tc.ctx, forbidden(), "kn", "ot")
			if got == nil || got.Shortfalls[0].RequestPermissionURL != "" {
				t.Fatalf("guidance = %+v, want the refusal without a link", got)
			}
		})
	}
}

func TestOnlyForbiddenErrorsGetGuidance(t *testing.T) {
	guide := newTestGuide(&schemaProbeStub{})
	for name, err := range map[string]error{
		"not found":   &infraErr.HTTPError{HTTPCode: http.StatusNotFound},
		"unavailable": &infraErr.HTTPError{HTTPCode: http.StatusServiceUnavailable},
		"transport":   errors.New("dial tcp: refused"),
	} {
		if got := guide.ForObjectTypeError(userCtx(""), err, "kn", "ot"); got != nil {
			t.Fatalf("%s: guidance = %+v, want nil", name, got)
		}
	}
}

func TestSuccessfulQueryReportsMaskingAndRowFilter(t *testing.T) {
	guide := newTestGuide(&schemaProbeStub{})

	got := guide.ForObjectQuery(userCtx("https://bkn.example.com"), "kn", "ot", map[string]interfaces.PropertyAccessLevel{
		"name": interfaces.PropertyAccessFull, "phone": interfaces.PropertyAccessMasked, "salary": interfaces.PropertyAccessSchema,
	}, true)

	if got == nil || len(got.Shortfalls) != 2 {
		t.Fatalf("guidance = %+v, want property and row-filter shortfalls", got)
	}
	properties, rows := got.Shortfalls[0], got.Shortfalls[1]
	if properties.Scope != interfaces.PermissionScopePropertyGrants ||
		!reflect.DeepEqual(properties.Properties, []string{"phone", "salary"}) ||
		properties.RequestPermissionURL != "https://bkn.example.com/studio/knowledge-network/workspace/kn/object-types/ot/detail?requestPermission=3" {
		t.Fatalf("property shortfall = %+v", properties)
	}
	if rows.Scope != interfaces.PermissionScopeRowFilter ||
		rows.RequestPermissionURL != "https://bkn.example.com/studio/knowledge-network/workspace/kn/object-types/ot/detail?requestPermission=2" {
		t.Fatalf("row-filter shortfall = %+v", rows)
	}
}

func TestUnrestrictedQueryGetsNoGuidance(t *testing.T) {
	probe := &schemaProbeStub{}
	guide := newTestGuide(probe)

	got := guide.ForObjectQuery(userCtx(""), "kn", "ot",
		map[string]interfaces.PropertyAccessLevel{"name": interfaces.PropertyAccessFull}, false)

	if got != nil || probe.calls != 0 {
		t.Fatalf("guidance = %+v, probes = %d; want nil and no probe", got, probe.calls)
	}
}

func TestVisibilityProbeIsCachedPerCallerUntilExpiry(t *testing.T) {
	probe := &schemaProbeStub{}
	guide := newTestGuide(probe)
	now := time.Unix(1_700_000_000, 0)
	guide.now = func() time.Time { return now }
	masked := map[string]interfaces.PropertyAccessLevel{"phone": interfaces.PropertyAccessMasked}

	guide.ForObjectQuery(userCtx(""), "kn", "ot", masked, false)
	guide.ForObjectQuery(userCtx(""), "kn", "ot", masked, false)
	if probe.calls != 1 {
		t.Fatalf("probes = %d, want 1 within the TTL", probe.calls)
	}

	other := common.SetAccountAuthContextToCtx(context.Background(), &interfaces.AccountAuthContext{
		AccountID: "u-2", AccountType: interfaces.AccessorTypeUser,
	})
	guide.ForObjectQuery(other, "kn", "ot", masked, false)
	if probe.calls != 2 {
		t.Fatalf("probes = %d, want a separate probe for another caller", probe.calls)
	}

	now = now.Add(visibilityTTL + time.Second)
	guide.ForObjectQuery(userCtx(""), "kn", "ot", masked, false)
	if probe.calls != 3 {
		t.Fatalf("probes = %d, want a new probe after expiry", probe.calls)
	}
}

func TestFailedVisibilityProbeIsNotCached(t *testing.T) {
	probe := &schemaProbeStub{err: &infraErr.HTTPError{HTTPCode: http.StatusServiceUnavailable}}
	guide := newTestGuide(probe)

	first := guide.ForObjectTypeError(userCtx(""), forbidden(), "kn", "ot")
	probe.err = nil
	second := guide.ForObjectTypeError(userCtx(""), forbidden(), "kn", "ot")

	if first.Shortfalls[0].RequestPermissionURL != "" || second.Shortfalls[0].RequestPermissionURL == "" {
		t.Fatalf("first = %+v, second = %+v; an outage must not suppress later links", first, second)
	}
}

func TestIDsAreEscapedIntoThePath(t *testing.T) {
	guide := newTestGuide(&schemaProbeStub{})

	got := guide.ForObjectTypeError(userCtx(""), forbidden(), "kn/../x", "o t")

	if path := got.Shortfalls[0].RequestPermissionURL; path !=
		"/studio/knowledge-network/workspace/kn%2F..%2Fx/object-types/o%20t/detail?requestPermission=1" {
		t.Fatalf("path = %q", path)
	}
}
