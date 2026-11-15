// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knskills

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

func TestTranslateLegacyEntryShell(t *testing.T) {
	cases := []struct {
		command   string
		wantEntry string
		wantArgs  []string
		wantOK    bool
	}{
		{"python main.py", "main.py", nil, true},
		{"python3 scripts/main.py --input contract.txt", "scripts/main.py", []string{"--input", "contract.txt"}, true},
		{"/usr/bin/python main.py -n 3", "main.py", []string{"-n", "3"}, true},
		{"main.py --flag", "main.py", []string{"--flag"}, true},
		{`python main.py --title "a b" 'c d'`, "main.py", []string{"--title", "a b", "c d"}, true},
		{`python main.py ""`, "main.py", []string{""}, true},
		// Not translatable: forwarded as entry_shell so the factory explains the refusal.
		{"bash run.sh", "", nil, false},
		{"python", "", nil, false},
		{"python run.sh", "", nil, false},
		{"python main.py && rm -rf x", "", nil, false},
		{"python main.py | cat", "", nil, false},
		{"python main.py $HOME", "", nil, false},
		{`python main.py "unterminated`, "", nil, false},
		{"", "", nil, false},
	}
	for _, tc := range cases {
		entry, args, ok := translateLegacyEntryShell(tc.command)
		if ok != tc.wantOK || entry != tc.wantEntry || len(args) != len(tc.wantArgs) || (len(args) > 0 && !reflect.DeepEqual(args, tc.wantArgs)) {
			t.Errorf("translateLegacyEntryShell(%q) = (%q, %#v, %v), want (%q, %#v, %v)",
				tc.command, entry, args, ok, tc.wantEntry, tc.wantArgs, tc.wantOK)
		}
	}
}

func TestExecuteSkillRequestForms(t *testing.T) {
	ctx := context.Background()
	run := func(req *ExecuteSkillReq) (*fakeOperator, error) {
		fake := &fakeOperator{execResp: &interfaces.ExecuteSkillResponse{SkillID: "sk-1"}}
		req.KnID, req.SkillID = "kn1", "sk-1"
		_, err := newTestService(fake, "sk-1").ExecuteSkill(ctx, req)
		return fake, err
	}

	t.Run("entry_point and arguments are sent as given", func(t *testing.T) {
		fake, err := run(&ExecuteSkillReq{EntryPoint: " scripts/main.py ", Arguments: []string{"--x", "1"}, Timeout: 20})
		if err != nil {
			t.Fatal(err)
		}
		got := fake.gotExecReq
		if got.EntryPoint != "scripts/main.py" || !reflect.DeepEqual(got.Arguments, []string{"--x", "1"}) ||
			got.EntryShell != "" || got.Timeout != 20 {
			t.Fatalf("request = %+v", got)
		}
	})

	t.Run("legacy python entry_shell is translated", func(t *testing.T) {
		fake, err := run(&ExecuteSkillReq{EntryShell: " python3 main.py --input 'a b' "})
		if err != nil {
			t.Fatal(err)
		}
		got := fake.gotExecReq
		if got.EntryPoint != "main.py" || !reflect.DeepEqual(got.Arguments, []string{"--input", "a b"}) || got.EntryShell != "" {
			t.Fatalf("request = %+v", got)
		}
	})

	t.Run("untranslatable entry_shell is forwarded for the factory to refuse", func(t *testing.T) {
		fake, err := run(&ExecuteSkillReq{EntryShell: "bash run.sh"})
		if err != nil {
			t.Fatal(err)
		}
		got := fake.gotExecReq
		if got.EntryShell != "bash run.sh" || got.EntryPoint != "" || len(got.Arguments) != 0 {
			t.Fatalf("request = %+v", got)
		}
	})

	t.Run("an entry is required", func(t *testing.T) {
		fake, err := run(&ExecuteSkillReq{})
		if !errors.Is(err, ErrEntryPointRequired) || fake.gotExecReq != nil {
			t.Fatalf("err = %v, request sent = %v", err, fake.gotExecReq != nil)
		}
	})

	t.Run("both forms are refused before anything runs", func(t *testing.T) {
		fake, err := run(&ExecuteSkillReq{EntryPoint: "main.py", EntryShell: "python main.py"})
		if !errors.Is(err, ErrEntryPointConflict) || fake.gotExecReq != nil {
			t.Fatalf("err = %v, request sent = %v", err, fake.gotExecReq != nil)
		}
	})
}

func TestGetSkillContentReportsRuntime(t *testing.T) {
	ctx := context.Background()

	t.Run("executable skill lists its entry points and adds no warning", func(t *testing.T) {
		fake := &fakeOperator{contentResp: &interfaces.GetSkillContentResponse{
			SkillID: "sk-1", Content: []byte("# s"),
			Runtime: &interfaces.SkillRuntime{Runtime: "python", Executable: true, EntryPoints: []string{"scripts/main.py"}},
		}}
		resp, err := newTestService(fake, "sk-1").GetSkillContent(ctx, "kn1", "sk-1")
		if err != nil {
			t.Fatal(err)
		}
		if resp.Runtime == nil || !resp.Runtime.Executable || !reflect.DeepEqual(resp.Runtime.EntryPoints, []string{"scripts/main.py"}) {
			t.Fatalf("runtime = %+v", resp.Runtime)
		}
		if resp.Message != "" {
			t.Fatalf("message = %q, want none", resp.Message)
		}
	})

	t.Run("not executable skill says so with the reason and no execution step", func(t *testing.T) {
		const reason = "the package has Shell/Bash scripts only"
		fake := &fakeOperator{contentResp: &interfaces.GetSkillContentResponse{
			SkillID: "sk-1", Content: []byte("# run bash run.sh"),
			Runtime: &interfaces.SkillRuntime{Runtime: "python", Executable: false, Reason: reason},
		}}
		resp, err := newTestService(fake, "sk-1").GetSkillContent(ctx, "kn1", "sk-1")
		if err != nil {
			t.Fatal(err)
		}
		if resp.Runtime == nil || resp.Runtime.Executable || resp.Runtime.Reason != reason || len(resp.Runtime.EntryPoints) != 0 {
			t.Fatalf("runtime = %+v", resp.Runtime)
		}
		if !strings.Contains(resp.Message, reason) {
			t.Fatalf("message = %q, want it to carry the reason", resp.Message)
		}
	})

	t.Run("a factory without runtime leaves the field out", func(t *testing.T) {
		fake := &fakeOperator{contentResp: &interfaces.GetSkillContentResponse{SkillID: "sk-1", Content: []byte("# s")}}
		resp, err := newTestService(fake, "sk-1").GetSkillContent(ctx, "kn1", "sk-1")
		if err != nil {
			t.Fatal(err)
		}
		if resp.Runtime != nil || resp.Message != "" {
			t.Fatalf("runtime = %+v message = %q", resp.Runtime, resp.Message)
		}
	})
}
