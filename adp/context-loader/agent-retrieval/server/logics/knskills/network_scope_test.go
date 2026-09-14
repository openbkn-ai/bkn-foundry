// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knskills

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// proxyOperator adds the explicit-account Skill reads to fakeOperator.
type proxyOperator struct {
	*fakeOperator

	proxyContent *interfaces.GetSkillContentResponse
	proxyFile    *interfaces.ReadSkillFileResponse
	proxyErr     error

	proxyCalls   int
	gotAccount   interfaces.AccountAuthContext
	gotProxyFile *interfaces.ReadSkillFileRequest
}

func (p *proxyOperator) GetSkillContentAs(_ context.Context, account interfaces.AccountAuthContext,
	_ string) (*interfaces.GetSkillContentResponse, error) {
	p.proxyCalls++
	p.gotAccount = account
	return p.proxyContent, p.proxyErr
}

func (p *proxyOperator) ReadSkillFileAs(_ context.Context, account interfaces.AccountAuthContext,
	req *interfaces.ReadSkillFileRequest) (*interfaces.ReadSkillFileResponse, error) {
	p.proxyCalls++
	p.gotAccount = account
	p.gotProxyFile = req
	return p.proxyFile, p.proxyErr
}

// lineLogger keeps the Info lines written through it.
type lineLogger struct {
	interfaces.Logger
	lines []string
}

func (l *lineLogger) WithContext(context.Context) interfaces.Logger { return l }

func (l *lineLogger) Infof(format string, args ...interface{}) {
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

// resolvingBkn adds bkn-backend's proxy resolution to fakeBkn.
type resolvingBkn struct {
	*fakeBkn

	resolveErr  error
	accountType string
	resolved    int
	gotBinding  interfaces.KNProxyBinding
}

func (r *resolvingBkn) ResolveKNProxyBinding(_ context.Context,
	binding interfaces.KNProxyBinding) (*interfaces.KNProxyAccount, error) {
	r.resolved++
	r.gotBinding = binding
	if r.resolveErr != nil {
		return nil, r.resolveErr
	}
	accountType := r.accountType
	if accountType == "" {
		accountType = "app"
	}
	return &interfaces.KNProxyAccount{KNID: binding.KNID, ProxyAccountID: "proxy-1", ProxyAccountType: accountType,
		LifecycleStatus: "active", SyncStatus: "ready", Version: 4}, nil
}

func mountedWithID(bindingID, skillID string) []*interfaces.CapabilityRef {
	return []*interfaces.CapabilityRef{{ID: bindingID, CapabilityType: interfaces.CapabilityTypeSkill, CapabilityID: skillID}}
}

func forbidden(ctx context.Context) error {
	return infraErr.DefaultHTTPError(ctx, http.StatusForbidden, "denied")
}

func detailOf(t *testing.T, err error) (int, string) {
	t.Helper()
	httpErr, ok := err.(*infraErr.HTTPError)
	if !ok {
		t.Fatalf("error = %v, want an HTTP error", err)
	}
	detail, _ := httpErr.ErrorDetails.(string)
	return httpErr.HTTPCode, detail
}

func skillContent() *interfaces.GetSkillContentResponse {
	return &interfaces.GetSkillContentResponse{
		SkillID: "sk-1", Content: []byte("# SKILL"), Status: "published",
		Files: []interfaces.SkillFileSummary{{RelPath: "SKILL.md"}},
	}
}

func TestCallerWithSkillGrantKeepsTheDirectRead(t *testing.T) {
	op := &proxyOperator{fakeOperator: &fakeOperator{contentResp: skillContent()}}
	bkn := &resolvingBkn{fakeBkn: &fakeBkn{refs: mountedWithID("binding-1", "sk-1")}}
	svc := NewKnSkillsServiceWith(op, bkn, &fakeKnAuthz{})

	resp, err := svc.GetSkillContent(context.Background(), "kn-1", "sk-1")
	if err != nil || resp.Content != "# SKILL" {
		t.Fatalf("GetSkillContent() = (%+v, %v)", resp, err)
	}
	if op.proxyCalls != 0 || bkn.resolved != 0 {
		t.Fatal("a caller the execution factory already serves went through the network proxy")
	}
}

func TestNetworkViewerReadsAMountedSkillAsTheProxyAccount(t *testing.T) {
	ctx := common.SetAccountAuthContextToCtx(context.Background(), &interfaces.AccountAuthContext{
		AccountID: "user-1", AccountType: interfaces.AccessorTypeUser,
	})
	op := &proxyOperator{
		fakeOperator: &fakeOperator{err: forbidden(ctx)},
		proxyContent: skillContent(),
		proxyFile:    &interfaces.ReadSkillFileResponse{SkillID: "sk-1", RelPath: "refs/a.md", MimeType: "text/markdown", Content: []byte("body")},
	}
	bkn := &resolvingBkn{fakeBkn: &fakeBkn{refs: mountedWithID("binding-1", "sk-1")}}
	logger := &lineLogger{}
	svc := &knSkillsService{operator: op, bknBackend: bkn, knAuthz: &fakeKnAuthz{}, logger: logger}

	content, err := svc.GetSkillContent(ctx, "kn-1", "sk-1")
	if err != nil || content.Content != "# SKILL" || len(content.Files) != 1 {
		t.Fatalf("GetSkillContent() = (%+v, %v)", content, err)
	}
	want := interfaces.KNProxyBinding{
		KNID: "kn-1", ChildType: interfaces.KNProxyChildTypeCapability, ChildID: "binding-1",
		TargetType: interfaces.KNProxyTargetTypeSkill, TargetID: "sk-1", Operation: interfaces.KNProxyOperationExecute,
	}
	proxy := interfaces.AccountAuthContext{AccountID: "proxy-1", AccountType: interfaces.AccessorTypeApp}
	if bkn.gotBinding != want || op.gotAccount != proxy {
		t.Fatalf("binding = %+v, account = %+v; want the mount's own binding read as the proxy", bkn.gotBinding, op.gotAccount)
	}

	file, err := svc.ReadSkillFile(ctx, &ReadSkillFileReq{KnID: "kn-1", SkillID: "sk-1", RelPath: "refs/a.md"})
	if err != nil || file.Content != "body" || op.gotProxyFile.RelPath != "refs/a.md" || op.gotAccount != proxy {
		t.Fatalf("ReadSkillFile() = (%+v, %v), proxied request %+v as %+v", file, err, op.gotProxyFile, op.gotAccount)
	}

	if len(logger.lines) != 2 {
		t.Fatalf("log lines = %q, want one per proxied read", logger.lines)
	}
	for _, line := range logger.lines {
		for _, field := range []string{"caller_id=user-1", "kn_id=kn-1", "proxy_account_id=proxy-1", "skill_id=sk-1"} {
			if !strings.Contains(line, field) {
				t.Fatalf("log line %q lacks %s", line, field)
			}
		}
	}
}

func TestProxyReadRefusesAMappingThatIsNotAnApp(t *testing.T) {
	ctx := context.Background()
	op := &proxyOperator{fakeOperator: &fakeOperator{err: forbidden(ctx)}, proxyContent: skillContent()}
	bkn := &resolvingBkn{fakeBkn: &fakeBkn{refs: mountedWithID("binding-1", "sk-1")}, accountType: "user"}

	_, err := NewKnSkillsServiceWith(op, bkn, &fakeKnAuthz{}).GetSkillContent(ctx, "kn-1", "sk-1")
	if status, _ := detailOf(t, err); status != http.StatusServiceUnavailable || op.proxyCalls != 0 {
		t.Fatalf("status = %d, proxy reads = %d; want 503 before any read", status, op.proxyCalls)
	}
}

func TestProxiedSkillReadRefusalsAreActionable(t *testing.T) {
	ctx := context.Background()
	tests := map[string]struct {
		resolveErr error
		proxyErr   error
		refs       []*interfaces.CapabilityRef
		status     int
		detail     string
	}{
		"proxy holds no grant": {proxyErr: forbidden(ctx), status: http.StatusForbidden,
			detail: infraErr.LocalizedDetail(ctx, "SkillNotProvisionedForNetwork")},
		"binding unknown to bkn-backend": {resolveErr: forbidden(ctx), status: http.StatusForbidden,
			detail: infraErr.LocalizedDetail(ctx, "SkillNotProvisionedForNetwork")},
		"synchronization pending": {resolveErr: infraErr.DefaultHTTPError(ctx, http.StatusServiceUnavailable, "pending"),
			status: http.StatusServiceUnavailable, detail: "pending"},
		"mount without an id": {refs: mounted("sk-1"), status: http.StatusServiceUnavailable,
			detail: infraErr.LocalizedDetail(ctx, "SkillAuthorizationUnavailable")},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			refs := test.refs
			if refs == nil {
				refs = mountedWithID("binding-1", "sk-1")
			}
			op := &proxyOperator{fakeOperator: &fakeOperator{err: forbidden(ctx)}, proxyContent: skillContent(), proxyErr: test.proxyErr}
			bkn := &resolvingBkn{fakeBkn: &fakeBkn{refs: refs}, resolveErr: test.resolveErr}
			_, err := NewKnSkillsServiceWith(op, bkn, &fakeKnAuthz{}).GetSkillContent(ctx, "kn-1", "sk-1")
			status, detail := detailOf(t, err)
			if status != test.status || detail != test.detail {
				t.Fatalf("error = %d %q, want %d %q", status, detail, test.status, test.detail)
			}
		})
	}
}

func TestOnlyACallerRefusalFallsBackToTheProxy(t *testing.T) {
	ctx := context.Background()
	op := &proxyOperator{fakeOperator: &fakeOperator{err: infraErr.DefaultHTTPError(ctx, http.StatusNotFound, "no file")}}
	bkn := &resolvingBkn{fakeBkn: &fakeBkn{refs: mountedWithID("binding-1", "sk-1")}}

	_, err := NewKnSkillsServiceWith(op, bkn, &fakeKnAuthz{}).
		ReadSkillFile(ctx, &ReadSkillFileReq{KnID: "kn-1", SkillID: "sk-1", RelPath: "missing.md"})
	if status, _ := detailOf(t, err); status != http.StatusNotFound {
		t.Fatalf("status = %d, want the direct 404", status)
	}
	if op.proxyCalls != 0 || bkn.resolved != 0 {
		t.Fatal("a missing file was retried through the proxy")
	}
}

func TestNetworkViewIsTheGateForSkillReads(t *testing.T) {
	ctx := context.Background()
	for name, test := range map[string]struct {
		authzErr error
		status   int
		detail   string
	}{
		// Child grants only, or a standalone grant on the Skill: the network itself is not viewable.
		"no network view": {forbidden(ctx), http.StatusForbidden, infraErr.LocalizedDetail(ctx, "CapabilityNetworkViewRequired")},
		"authorization unavailable": {infraErr.DefaultHTTPError(ctx, http.StatusServiceUnavailable, "down"),
			http.StatusServiceUnavailable, "down"},
	} {
		t.Run(name, func(t *testing.T) {
			op := &proxyOperator{fakeOperator: &fakeOperator{contentResp: skillContent()}, proxyContent: skillContent()}
			bkn := &resolvingBkn{fakeBkn: &fakeBkn{refs: mountedWithID("binding-1", "sk-1")}}
			svc := NewKnSkillsServiceWith(op, bkn, &fakeKnAuthz{err: test.authzErr})

			for _, err := range []error{
				func() error { _, err := svc.GetSkillContent(ctx, "kn-1", "sk-1"); return err }(),
				func() error {
					_, err := svc.ReadSkillFile(ctx, &ReadSkillFileReq{KnID: "kn-1", SkillID: "sk-1", RelPath: "a.md"})
					return err
				}(),
			} {
				status, detail := detailOf(t, err)
				if status != test.status || detail != test.detail {
					t.Fatalf("error = %d %q, want %d %q", status, detail, test.status, test.detail)
				}
			}
			if op.gotContentID != "" || op.gotFileReq != nil || op.proxyCalls != 0 || bkn.gotKN != "" {
				t.Fatal("a refused caller reached the mounts or the skill")
			}
		})
	}
}

func TestUnmountedSkillIsNotReadThroughTheProxy(t *testing.T) {
	ctx := context.Background()
	op := &proxyOperator{fakeOperator: &fakeOperator{err: forbidden(ctx)}, proxyContent: skillContent()}
	bkn := &resolvingBkn{fakeBkn: &fakeBkn{refs: mountedWithID("binding-1", "sk-other")}}

	_, err := NewKnSkillsServiceWith(op, bkn, &fakeKnAuthz{}).GetSkillContent(ctx, "kn-1", "sk-1")
	if _, detail := detailOf(t, err); detail != infraErr.LocalizedDetail(ctx, "SkillNotMountedOnNetwork") {
		t.Fatalf("detail = %q, want SkillNotMountedOnNetwork", detail)
	}
	if op.gotContentID != "" || op.proxyCalls != 0 || bkn.resolved != 0 {
		t.Fatal("an unmounted skill was read")
	}
}
