// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package permissionguide

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"strings"
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

// splitLink separates a request link into its route and query values.
func splitLink(t *testing.T, link string) (string, url.Values) {
	t.Helper()
	parsed, err := url.Parse(link)
	if err != nil {
		t.Fatalf("link %q does not parse: %v", link, err)
	}
	route := parsed.Path
	if parsed.Host != "" {
		route = parsed.Scheme + "://" + parsed.Host + route
	}
	return route, parsed.Query()
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

	link := got.Shortfalls[0].RequestPermissionURL
	got.Shortfalls[0].RequestPermissionURL = ""
	want := &interfaces.PermissionGuidance{
		Message: "你暂无「客户信息」的数据查询权限，无法完成本次查询。可通过申请权限链接提交申请；提交后请等待管理员完成授权，再重新发起任务。",
		Resource: interfaces.PermissionGuidanceResource{
			Type: "object_type", ID: "55555/6666", KnID: "55555", OtID: "6666", Name: "客户信息",
		},
		Shortfalls: []interfaces.PermissionShortfall{{
			Scope: interfaces.PermissionScopeGrant, Operations: []string{"query_data"},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("guidance = %+v\nwant %+v", got, want)
	}

	route, query := splitLink(t, link)
	if route != "https://bkn.example.com/studio/knowledge-network/workspace/55555/object-types/6666/detail" {
		t.Fatalf("route = %q", route)
	}
	wantQuery := url.Values{
		"requestPermission": {"1"},
		"operations":        {"query_data"},
		"reason":            {"智能体查询「客户信息」的数据时缺少数据查询权限"},
		"source":            {"agent"},
	}
	if !reflect.DeepEqual(query, wantQuery) {
		t.Fatalf("query = %v, want %v", query, wantQuery)
	}
}

func TestInClusterCallerGetsARelativeLink(t *testing.T) {
	guide := newTestGuide(&schemaProbeStub{})

	got := guide.ForObjectTypeError(userCtx(""), forbidden(), "kn", "ot")

	if route, _ := splitLink(t, got.Shortfalls[0].RequestPermissionURL); route !=
		"/studio/knowledge-network/workspace/kn/object-types/ot/detail" {
		t.Fatalf("route = %q, want the path on the Studio host", route)
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

	got := guide.ForObjectQuery(userCtx("https://bkn.example.com"), "kn", "ot", nil, map[string]interfaces.PropertyAccessLevel{
		"name": interfaces.PropertyAccessFull, "phone": interfaces.PropertyAccessMasked, "salary": interfaces.PropertyAccessSchema,
	}, true)

	if got == nil || len(got.Shortfalls) != 2 {
		t.Fatalf("guidance = %+v, want property and row-filter shortfalls", got)
	}
	properties, rows := got.Shortfalls[0], got.Shortfalls[1]
	if properties.Scope != interfaces.PermissionScopePropertyGrants ||
		!reflect.DeepEqual(properties.Properties, []string{"phone", "salary"}) {
		t.Fatalf("property shortfall = %+v", properties)
	}
	if !strings.Contains(properties.RequestPermissionURL, "properties=phone,salary&") {
		t.Fatalf("property list must keep literal commas: %s", properties.RequestPermissionURL)
	}
	_, query := splitLink(t, properties.RequestPermissionURL)
	if want := (url.Values{
		"requestPermission": {"3"}, "properties": {"phone,salary"},
		"reason": {"智能体需要读取「客户信息」中字段 phone、salary 的原始值"}, "source": {"agent"},
	}); !reflect.DeepEqual(query, want) {
		t.Fatalf("property link query = %v, want %v", query, want)
	}
	if rows.Scope != interfaces.PermissionScopeRowFilter {
		t.Fatalf("row-filter shortfall = %+v", rows)
	}
	_, query = splitLink(t, rows.RequestPermissionURL)
	if want := (url.Values{
		"requestPermission": {"2"},
		"reason":            {"智能体查询「客户信息」时受行访问范围限制，需要扩大访问范围"}, "source": {"agent"},
	}); !reflect.DeepEqual(query, want) {
		t.Fatalf("row-filter link query = %v, want %v", query, want)
	}
	if strings.Contains(rows.RequestPermissionURL, "+") {
		t.Fatalf("spaces must be encoded as %%20, not +: %s", rows.RequestPermissionURL)
	}
}

func TestUnrestrictedQueryGetsNoGuidance(t *testing.T) {
	probe := &schemaProbeStub{}
	guide := newTestGuide(probe)

	got := guide.ForObjectQuery(userCtx(""), "kn", "ot", nil,
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

	guide.ForObjectQuery(userCtx(""), "kn", "ot", nil, masked, false)
	guide.ForObjectQuery(userCtx(""), "kn", "ot", nil, masked, false)
	if probe.calls != 1 {
		t.Fatalf("probes = %d, want 1 within the TTL", probe.calls)
	}

	other := common.SetAccountAuthContextToCtx(context.Background(), &interfaces.AccountAuthContext{
		AccountID: "u-2", AccountType: interfaces.AccessorTypeUser,
	})
	guide.ForObjectQuery(other, "kn", "ot", nil, masked, false)
	if probe.calls != 2 {
		t.Fatalf("probes = %d, want a separate probe for another caller", probe.calls)
	}

	now = now.Add(visibilityTTL + time.Second)
	guide.ForObjectQuery(userCtx(""), "kn", "ot", nil, masked, false)
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

	if link := got.Shortfalls[0].RequestPermissionURL; !strings.HasPrefix(link,
		"/studio/knowledge-network/workspace/kn%2F..%2Fx/object-types/o%20t/detail?requestPermission=1&") {
		t.Fatalf("link = %q", link)
	}
}

func TestMessageAsksUnlinkedCallersToContactAnAdministrator(t *testing.T) {
	guide := newTestGuide(&schemaProbeStub{err: forbidden()})

	got := guide.ForObjectTypeError(userCtx(""), forbidden(), "kn", "ot")

	if want := "你暂无「该对象类」的数据查询权限，无法完成本次查询。请联系管理员授权。"; got.Message != want {
		t.Fatalf("message = %q, want %q", got.Message, want)
	}
}

func TestMessageFollowsTheRequestLanguage(t *testing.T) {
	guide := newTestGuide(&schemaProbeStub{})
	ctx := common.SetLanguageToCtx(userCtx(""), common.Language("en-US"))

	got := guide.ForObjectQuery(ctx, "kn", "ot", nil, map[string]interfaces.PropertyAccessLevel{
		"phone": interfaces.PropertyAccessMasked, "salary": interfaces.PropertyAccessSchema,
	}, true)

	want := "In “客户信息”, the raw values of phone, salary are not available to you. " +
		"You can access only part of the data in “客户信息”, so the result may be incomplete. " +
		"You can submit a request through the request link; after submitting, wait for an administrator to approve it, then rerun the task."
	if got.Message != want {
		t.Fatalf("message = %q\nwant      %q", got.Message, want)
	}
	_, query := splitLink(t, got.Shortfalls[0].RequestPermissionURL)
	if reason := query.Get("reason"); reason != "The agent needs raw values in “客户信息” for phone, salary" {
		t.Fatalf("reason = %q", reason)
	}
}

func TestOnlyRequestedPropertiesAreReportedAsWithheld(t *testing.T) {
	guide := newTestGuide(&schemaProbeStub{})
	effective := map[string]interfaces.PropertyAccessLevel{
		"name": interfaces.PropertyAccessFull, "phone": interfaces.PropertyAccessMasked, "salary": interfaces.PropertyAccessSchema,
	}

	if got := guide.ForObjectQuery(userCtx(""), "kn", "ot", []string{"name"}, effective, false); got != nil {
		t.Fatalf("guidance = %+v, want nil when only full properties were requested", got)
	}
	got := guide.ForObjectQuery(userCtx(""), "kn", "ot", []string{"name", "phone"}, effective, false)
	if got == nil || !reflect.DeepEqual(got.Shortfalls[0].Properties, []string{"phone"}) {
		t.Fatalf("guidance = %+v, want only the requested masked property", got)
	}
}

func TestDenialGuidanceRequiresTheObjectTypeInTheRefusal(t *testing.T) {
	guide := newTestGuide(&schemaProbeStub{})
	named := &infraErr.HTTPError{HTTPCode: http.StatusForbidden,
		ErrorDetails: "Public.Forbidden: query_data was not granted for object_type:kn/ot"}
	other := &infraErr.HTTPError{HTTPCode: http.StatusForbidden,
		ErrorDetails: "Public.Forbidden: execute was not granted for metric:m-1"}

	if got := guide.ForObjectTypeDenial(userCtx(""), named, "kn", "ot"); got == nil {
		t.Fatal("a refusal naming the object type must carry guidance")
	}
	if got := guide.ForObjectTypeDenial(userCtx(""), other, "kn", "ot"); got != nil {
		t.Fatalf("guidance = %+v, want nil for a refusal of another resource", got)
	}
}
