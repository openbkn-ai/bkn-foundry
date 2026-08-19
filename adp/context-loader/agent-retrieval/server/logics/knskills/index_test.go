// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knskills

import (
	"context"
	"errors"
	"strings"
	"testing"

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

	gotListReq *interfaces.ListSkillsRequest
	gotFileReq *interfaces.ReadSkillFileRequest
	gotExecReq *interfaces.ExecuteSkillRequest
}

func (f *fakeOperator) ListSkills(_ context.Context, req *interfaces.ListSkillsRequest) (*interfaces.ListSkillsResponse, error) {
	f.gotListReq = req
	return f.listResp, f.err
}

func (f *fakeOperator) GetSkillContent(_ context.Context, _ string) (*interfaces.GetSkillContentResponse, error) {
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

func TestListSkillsExplainsEmptyResult(t *testing.T) {
	fake := &fakeOperator{listResp: &interfaces.ListSkillsResponse{}}
	svc := NewKnSkillsServiceWith(fake)

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

	resp, err := NewKnSkillsServiceWith(fake).GetSkillContent(context.Background(), "sk-1")
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

	resp, err := NewKnSkillsServiceWith(fake).ReadSkillFile(context.Background(),
		&ReadSkillFileReq{SkillID: "sk-1", RelPath: "assets/logo.png"})
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

	resp, err := NewKnSkillsServiceWith(fake).ReadSkillFile(context.Background(),
		&ReadSkillFileReq{SkillID: "sk-1", RelPath: "refs/guide.md"})
	if err != nil {
		t.Fatalf("ReadSkillFile: %v", err)
	}
	if resp.Content != "# 指南\n正文" {
		t.Fatalf("正文被误判为二进制: %q / %s", resp.Content, resp.Message)
	}
}

func TestReadSkillFileRequiresRelPath(t *testing.T) {
	svc := NewKnSkillsServiceWith(&fakeOperator{})

	if _, err := svc.ReadSkillFile(context.Background(), &ReadSkillFileReq{SkillID: "sk-1"}); !errors.Is(err, ErrRelPathRequired) {
		t.Fatalf("缺 rel_path 时 err = %v，want ErrRelPathRequired", err)
	}
	if _, err := svc.ReadSkillFile(context.Background(), &ReadSkillFileReq{RelPath: "a.md"}); !errors.Is(err, ErrSkillIDRequired) {
		t.Fatalf("缺 skill_id 时 err = %v，want ErrSkillIDRequired", err)
	}
}

func TestExecuteSkillRequiresEntryShellAndTruncatesStreams(t *testing.T) {
	svc := NewKnSkillsServiceWith(&fakeOperator{})
	if _, err := svc.ExecuteSkill(context.Background(), &ExecuteSkillReq{SkillID: "sk-1"}); !errors.Is(err, ErrEntryShellRequired) {
		t.Fatalf("缺 entry_shell 时 err = %v，want ErrEntryShellRequired", err)
	}

	fake := &fakeOperator{execResp: &interfaces.ExecuteSkillResponse{
		SkillID:  "sk-1",
		ExitCode: 0,
		Stdout:   strings.Repeat("x", maxStreamChars+1),
		Stderr:   "warn",
	}}
	resp, err := NewKnSkillsServiceWith(fake).ExecuteSkill(context.Background(),
		&ExecuteSkillReq{SkillID: "sk-1", EntryShell: " python main.py ", Timeout: 30})
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
