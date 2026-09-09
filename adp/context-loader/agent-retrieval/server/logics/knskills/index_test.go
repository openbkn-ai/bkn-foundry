// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knskills

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// fakeOperator only implements the technical side, leaving the other methods blank - only these four are needed for this layer.
type fakeOperator struct {
	interfaces.DrivenOperatorIntegration

	listResp    *interfaces.ListSkillsResponse
	contentResp *interfaces.GetSkillContentResponse
	fileResp    *interfaces.ReadSkillFileResponse
	execResp    *interfaces.ExecuteSkillResponse
	err         error

	gotListReq   *interfaces.ListSkillsRequest
	gotFileReq   *interfaces.ReadSkillFileRequest
	gotExecReq   *interfaces.ExecuteSkillRequest
	gotContentID string
}

func (f *fakeOperator) ListSkills(_ context.Context, req *interfaces.ListSkillsRequest) (*interfaces.ListSkillsResponse, error) {
	f.gotListReq = req
	return f.listResp, f.err
}

func (f *fakeOperator) GetSkillContent(_ context.Context, skillID string) (*interfaces.GetSkillContentResponse, error) {
	f.gotContentID = skillID
	return f.contentResp, f.err
}

func (f *fakeOperator) ReadSkillFile(_ context.Context, req *interfaces.ReadSkillFileRequest) (*interfaces.ReadSkillFileResponse, error) {
	f.gotFileReq = req
	return f.fileResp, f.err
}

func (f *fakeOperator) ExecuteSkill(_ context.Context, req *interfaces.ExecuteSkillRequest) (*interfaces.ExecuteSkillResponse, error) {
	f.gotExecReq = req
	return f.execResp, f.err
}

// fakeBkn answers the capability listing that decides the scope.
type fakeBkn struct {
	interfaces.BknBackendAccess

	refs []*interfaces.CapabilityRef
	err  error

	gotKN string
}

func (f *fakeBkn) ListKNCapabilities(_ context.Context, knID, _, _ string,
) ([]*interfaces.CapabilityRef, error) {
	f.gotKN = knID
	return f.refs, f.err
}

// fakeKnAuthz stands in for the per-caller knowledge-network check.
type fakeKnAuthz struct {
	err     error
	gotKNID string
}

func (f *fakeKnAuthz) AuthorizeRead(_ context.Context, knID string) error {
	f.gotKNID = knID
	return f.err
}

// mounted builds a binding list holding these skill ids.
func mounted(ids ...string) []*interfaces.CapabilityRef {
	refs := make([]*interfaces.CapabilityRef, 0, len(ids))
	for _, id := range ids {
		refs = append(refs, &interfaces.CapabilityRef{
			CapabilityType: interfaces.CapabilityTypeSkill,
			CapabilityID:   id,
		})
	}
	return refs
}

// newTestService wires a service whose network has mounted every id the case uses.
func newTestService(op *fakeOperator, ids ...string) KnSkillsService {
	return NewKnSkillsServiceWith(op, &fakeBkn{refs: mounted(ids...)}, &fakeKnAuthz{})
}

func TestListSkillsExplainsEmptyResult(t *testing.T) {
	fake := &fakeOperator{listResp: &interfaces.ListSkillsResponse{}}
	svc := newTestService(fake)

	resp, err := svc.ListSkills(context.Background(), &ListSkillsReq{Name: "  合同  "})
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	if resp.Message == "" {
		t.Fatal("空结果没有给出说明，模型无从判断是没权限还是没匹配")
	}
	if fake.gotListReq.Name != "合同" {
		t.Fatalf("过滤条件未去空白: %q", fake.gotListReq.Name)
	}
}

func TestGetSkillContentTruncatesByRune(t *testing.T) {
	// Full Chinese text, truncation by bytes will chop up multi-byte characters.
	body := strings.Repeat("知", maxDocChars+10)
	fake := &fakeOperator{contentResp: &interfaces.GetSkillContentResponse{
		SkillID: "sk-1",
		Content: []byte(body),
		Files:   []interfaces.SkillFileSummary{{RelPath: "refs/guide.md"}},
	}}

	resp, err := newTestService(fake, "sk-1").GetSkillContent(context.Background(), "kn1", "sk-1")
	if err != nil {
		t.Fatalf("GetSkillContent: %v", err)
	}
	if !resp.Truncated || resp.Message == "" {
		t.Fatal("超长正文未标注截断")
	}
	if !strings.HasSuffix(resp.Content, "知") {
		t.Fatal("截断切碎了多字节字符")
	}
	if len([]rune(resp.Content)) != maxDocChars {
		t.Fatalf("截断长度 = %d 字符，want %d", len([]rune(resp.Content)), maxDocChars)
	}
	if len(resp.Files) != 1 || resp.Files[0].RelPath != "refs/guide.md" {
		t.Fatal("文件清单丢失，下钻链路断了")
	}
}

func TestReadSkillFileWithholdsBinaryBody(t *testing.T) {
	fake := &fakeOperator{fileResp: &interfaces.ReadSkillFileResponse{
		SkillID:  "sk-1",
		RelPath:  "assets/logo.png",
		MimeType: "image/png",
		Content:  []byte{0x89, 0x50, 0x4e, 0x47, 0x00, 0xff},
	}}

	resp, err := newTestService(fake, "sk-1").ReadSkillFile(context.Background(),
		&ReadSkillFileReq{KnID: "kn1", SkillID: "sk-1", RelPath: "assets/logo.png"})
	if err != nil {
		t.Fatalf("ReadSkillFile: %v", err)
	}
	if resp.Content != "" {
		t.Fatal("二进制正文被塞进了上下文")
	}
	if resp.Message == "" {
		t.Fatal("二进制文件未说明为何没有正文")
	}
}

func TestReadSkillFileKeepsUTF8Body(t *testing.T) {
	fake := &fakeOperator{fileResp: &interfaces.ReadSkillFileResponse{
		SkillID:  "sk-1",
		RelPath:  "refs/guide.md",
		MimeType: "application/octet-stream", // This is often the case in object storage, and binary data cannot be determined based on this.
		Content:  []byte("# 指南\n正文"),
	}}

	resp, err := newTestService(fake, "sk-1").ReadSkillFile(context.Background(),
		&ReadSkillFileReq{KnID: "kn1", SkillID: "sk-1", RelPath: "refs/guide.md"})
	if err != nil {
		t.Fatalf("ReadSkillFile: %v", err)
	}
	if resp.Content != "# 指南\n正文" {
		t.Fatalf("正文被误判为二进制: %q / %s", resp.Content, resp.Message)
	}
}

func TestReadSkillFileRequiresRelPath(t *testing.T) {
	svc := newTestService(&fakeOperator{}, "sk-1")

	if _, err := svc.ReadSkillFile(context.Background(), &ReadSkillFileReq{KnID: "kn1", SkillID: "sk-1"}); !errors.Is(err, ErrRelPathRequired) {
		t.Fatalf("缺 rel_path 时 err = %v，want ErrRelPathRequired", err)
	}
	if _, err := svc.ReadSkillFile(context.Background(), &ReadSkillFileReq{KnID: "kn1", RelPath: "a.md"}); !errors.Is(err, ErrSkillIDRequired) {
		t.Fatalf("缺 skill_id 时 err = %v，want ErrSkillIDRequired", err)
	}
}

func TestExecuteSkillRequiresEntryShellAndTruncatesStreams(t *testing.T) {
	svc := newTestService(&fakeOperator{}, "sk-1")
	if _, err := svc.ExecuteSkill(context.Background(), &ExecuteSkillReq{KnID: "kn1", SkillID: "sk-1"}); !errors.Is(err, ErrEntryShellRequired) {
		t.Fatalf("缺 entry_shell 时 err = %v，want ErrEntryShellRequired", err)
	}

	fake := &fakeOperator{execResp: &interfaces.ExecuteSkillResponse{
		SkillID:  "sk-1",
		ExitCode: 0,
		Stdout:   strings.Repeat("x", maxStreamChars+1),
		Stderr:   "warn",
	}}
	resp, err := newTestService(fake, "sk-1").ExecuteSkill(context.Background(),
		&ExecuteSkillReq{KnID: "kn1", SkillID: "sk-1", EntryShell: " python main.py ", Timeout: 30})
	if err != nil {
		t.Fatalf("ExecuteSkill: %v", err)
	}
	if !resp.Truncated || len(resp.Stdout) != maxStreamChars {
		t.Fatalf("stdout 未按上限截断: truncated=%v len=%d", resp.Truncated, len(resp.Stdout))
	}
	if fake.gotExecReq.EntryShell != "python main.py" {
		t.Fatalf("entry_shell 未去空白: %q", fake.gotExecReq.EntryShell)
	}
	if fake.gotExecReq.Timeout != 30 {
		t.Fatalf("timeout 未透传: %d", fake.gotExecReq.Timeout)
	}
}

// TestUnmountedSkillIsRefusedOnEveryEntry is the control this change exists for. Narrowing
// recall is not enough on its own: a skill_id outlives the call that produced it, and list_skills
// hands out every id on the platform.
func TestUnmountedSkillIsRefusedOnEveryEntry(t *testing.T) {
	newSvc := func() (KnSkillsService, *fakeOperator) {
		op := &fakeOperator{
			contentResp: &interfaces.GetSkillContentResponse{SkillID: "other"},
			fileResp:    &interfaces.ReadSkillFileResponse{},
			execResp:    &interfaces.ExecuteSkillResponse{},
		}
		return NewKnSkillsServiceWith(op, &fakeBkn{refs: mounted("mounted-skill")},
			&fakeKnAuthz{}), op
	}

	svc, op := newSvc()
	if _, err := svc.GetSkillContent(context.Background(), "kn1", "not-mounted"); err == nil {
		t.Fatal("未挂载的 Skill 不该读到正文")
	} else if op.gotContentID != "" {
		t.Fatal("拒绝的请求不该打到执行工厂")
	}

	svc, op = newSvc()
	if _, err := svc.ReadSkillFile(context.Background(),
		&ReadSkillFileReq{KnID: "kn1", SkillID: "not-mounted", RelPath: "a.md"}); err == nil {
		t.Fatal("未挂载的 Skill 不该读到包内文件")
	} else if op.gotFileReq != nil {
		t.Fatal("拒绝的请求不该打到执行工厂")
	}

	svc, op = newSvc()
	if _, err := svc.ExecuteSkill(context.Background(),
		&ExecuteSkillReq{KnID: "kn1", SkillID: "not-mounted", EntryShell: "python main.py"}); err == nil {
		t.Fatal("未挂载的 Skill 不该被执行")
	} else if op.gotExecReq != nil {
		t.Fatal("未挂载的 Skill 绝不能到达沙箱")
	}
}

// TestMountedSkillStillWorks guards the regression side: the gate must not break the path it
// protects.
func TestMountedSkillStillWorks(t *testing.T) {
	op := &fakeOperator{contentResp: &interfaces.GetSkillContentResponse{
		SkillID: "sk-1", Content: []byte("# SKILL"),
	}}
	svc := NewKnSkillsServiceWith(op, &fakeBkn{refs: mounted("sk-1")}, &fakeKnAuthz{})

	resp, err := svc.GetSkillContent(context.Background(), "kn1", "sk-1")
	if err != nil {
		t.Fatalf("已挂载的 Skill 应当可读: %v", err)
	}
	if resp.SkillID != "sk-1" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

// TestKnIDIsRequired keeps the gate from being skipped by omitting the scope.
func TestKnIDIsRequired(t *testing.T) {
	op := &fakeOperator{}
	svc := NewKnSkillsServiceWith(op, &fakeBkn{refs: mounted("sk-1")}, &fakeKnAuthz{})

	// A missing argument must surface as a 400: an unclassified error becomes a 500, which reads
	// as a platform fault rather than a call the caller can fix.
	assert400 := func(err error, what string) {
		t.Helper()
		var he *infraErr.HTTPError
		if !errors.As(err, &he) || he.HTTPCode != http.StatusBadRequest {
			t.Fatalf("%s: err = %v，want 400", what, err)
		}
	}
	_, err := svc.GetSkillContent(context.Background(), "", "sk-1")
	assert400(err, "get_skill_content 缺 kn_id")
	_, err = svc.ExecuteSkill(context.Background(), &ExecuteSkillReq{SkillID: "sk-1", EntryShell: "x"})
	assert400(err, "execute_skill 缺 kn_id")
	if op.gotExecReq != nil {
		t.Fatal("没有范围就不该执行任何东西")
	}
}

// TestUnauthorizedNetworkIsRefused: the bindings are read with this service's identity, so the
// caller's right to read the network has to be checked separately.
func TestUnauthorizedNetworkIsRefused(t *testing.T) {
	op := &fakeOperator{}
	bkn := &fakeBkn{refs: mounted("sk-1")}
	svc := NewKnSkillsServiceWith(op, bkn, &fakeKnAuthz{err: errors.New("forbidden")})

	if _, err := svc.ExecuteSkill(context.Background(),
		&ExecuteSkillReq{KnID: "someone-elses-kn", SkillID: "sk-1", EntryShell: "x"}); err == nil {
		t.Fatal("无权访问的网络应当被拒绝")
	}
	if bkn.gotKN != "" {
		t.Fatal("授权未通过时不该去读绑定")
	}
	if op.gotExecReq != nil {
		t.Fatal("授权未通过时不该执行")
	}
}

// TestMissingGateFailsClosed: a service wired without the check must refuse, not proceed.
func TestMissingGateFailsClosed(t *testing.T) {
	op := &fakeOperator{}
	svc := NewKnSkillsServiceWith(op, nil, nil)

	if _, err := svc.ExecuteSkill(context.Background(),
		&ExecuteSkillReq{KnID: "kn1", SkillID: "sk-1", EntryShell: "x"}); err == nil {
		t.Fatal("没接授权器的服务应当拒绝")
	}
	if op.gotExecReq != nil {
		t.Fatal("没接授权器时不该执行")
	}
}
