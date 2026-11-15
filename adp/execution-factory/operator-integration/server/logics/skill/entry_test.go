package skill

import (
	"context"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

var entryManifest = []string{"SKILL.md", "scripts/run.py", "main.py", "scripts/run.sh"}

func resolve(t *testing.T, req *interfaces.ExecuteSkillReq) (string, []string, error) {
	t.Helper()
	return resolveSkillEntry(context.Background(), req, entryManifest)
}

func TestResolveSkillEntryAcceptsPythonEntryPointWithArgv(t *testing.T) {
	entry, args, err := resolve(t, &interfaces.ExecuteSkillReq{EntryPoint: "scripts/run.py", Arguments: []string{"send", "--text", "a b (rm -rf /)"}})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if entry != "scripts/run.py" || len(args) != 3 || args[2] != "a b (rm -rf /)" {
		t.Fatalf("entry=%q args=%q", entry, args)
	}
	// Arguments are data: the shell metacharacters (other than the command separators, which are refused) stay inside one quoted word.
	got := pythonCommand(entry, args)
	want := `python3 'scripts/run.py' 'send' '--text' 'a b (rm -rf /)'`
	if got != want {
		t.Fatalf("command = %s, want %s", got, want)
	}
}

func TestResolveSkillEntryRejectsBashAndShell(t *testing.T) {
	for _, cmd := range []string{"bash run.sh", "sh run.sh", "./run.sh", "curl -X POST http://x", "bash -c 'echo hi'"} {
		if _, _, err := resolve(t, &interfaces.ExecuteSkillReq{EntryShell: cmd}); err == nil {
			t.Errorf("%q must be rejected", cmd)
		}
	}
	if _, _, err := resolve(t, &interfaces.ExecuteSkillReq{EntryPoint: "scripts/run.sh"}); err == nil {
		t.Error("a .sh entry_point must be rejected")
	}
}

func TestResolveSkillEntryRejectsPathTraversalAndAbsolutePaths(t *testing.T) {
	for _, entry := range []string{"../evil.py", "scripts/../../evil.py", "/etc/passwd.py", "/workspace/main.py", `scripts\run.py`, "scripts/run.py\x00.txt"} {
		if _, _, err := resolve(t, &interfaces.ExecuteSkillReq{EntryPoint: entry}); err == nil {
			t.Errorf("%q must be rejected", entry)
		}
	}
}

func TestResolveSkillEntryRequiresTheScriptToBeInTheManifest(t *testing.T) {
	_, _, err := resolve(t, &interfaces.ExecuteSkillReq{EntryPoint: "scripts/missing.py"})
	if err == nil || !strings.Contains(err.Error(), "not a file of this Skill") {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveSkillEntryKeepsTheLegacyPythonSpelling(t *testing.T) {
	entry, args, err := resolve(t, &interfaces.ExecuteSkillReq{EntryShell: `python main.py --input "my file.txt"`})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if entry != "main.py" || len(args) != 2 || args[1] != "my file.txt" {
		t.Fatalf("entry=%q args=%q", entry, args)
	}
}

func TestResolveSkillEntryRefusesShellSyntaxInTheLegacySpelling(t *testing.T) {
	for _, cmd := range []string{"python main.py; curl http://x", "python main.py | sh", "python main.py > /tmp/x", "python main.py $(id)", "python main.py && ls", "python 'main.py"} {
		if _, _, err := resolve(t, &interfaces.ExecuteSkillReq{EntryShell: cmd}); err == nil {
			t.Errorf("%q must be rejected", cmd)
		}
	}
}

func TestResolveSkillEntryNeedsOneEntry(t *testing.T) {
	if _, _, err := resolve(t, &interfaces.ExecuteSkillReq{}); err == nil {
		t.Error("an empty request must be rejected")
	}
	if _, _, err := resolve(t, &interfaces.ExecuteSkillReq{EntryPoint: "main.py", EntryShell: "python main.py"}); err == nil {
		t.Error("entry_point together with entry_shell must be rejected")
	}
}

func TestSkillRuntimeMarksShellOnlyPackagesNotExecutable(t *testing.T) {
	shellOnly := skillRuntimeOf([]*interfaces.SkillFileSummary{{RelPath: "SKILL.md"}, {RelPath: "scripts/send.sh"}})
	if shellOnly.Executable || shellOnly.Runtime != "python" || !strings.Contains(shellOnly.Reason, "Python") {
		t.Fatalf("shell-only runtime = %+v", shellOnly)
	}
	py := skillRuntimeOf([]*interfaces.SkillFileSummary{{RelPath: "SKILL.md"}, {RelPath: "scripts/send.py"}})
	if !py.Executable || len(py.EntryPoints) != 1 || py.EntryPoints[0] != "scripts/send.py" {
		t.Fatalf("python runtime = %+v", py)
	}
	docOnly := skillRuntimeOf([]*interfaces.SkillFileSummary{{RelPath: "SKILL.md"}})
	if docOnly.Executable || docOnly.Reason == "" {
		t.Fatalf("doc-only runtime = %+v", docOnly)
	}
}

func TestResolveSkillEntryRefusesCommandSeparatorsInArgumentsAndEntry(t *testing.T) {
	for _, arg := range []string{"a; bash cat b", "x & sh echo y", "a|b", "a\nb", "a\rb", "';'"} {
		_, _, err := resolve(t, &interfaces.ExecuteSkillReq{EntryPoint: "scripts/run.py", Arguments: []string{"ok", arg}})
		if err == nil || !strings.Contains(err.Error(), "must not contain") {
			t.Errorf("argument %q must be refused, err = %v", arg, err)
		}
	}
	for _, entry := range []string{"scripts/run.py; id", "scripts/run.py|x", "scripts/run.py&x", "scripts/run.py\nx"} {
		if _, _, err := resolve(t, &interfaces.ExecuteSkillReq{EntryPoint: entry}); err == nil {
			t.Errorf("entry_point %q must be refused", entry)
		}
	}
	// Legacy spelling: a separator inside quotes survives splitCommand but must still be refused.
	for _, cmd := range []string{`python main.py "a; bash cat b"`, `python main.py 'x & sh y'`, `python main.py "a|b"`} {
		if _, _, err := resolve(t, &interfaces.ExecuteSkillReq{EntryShell: cmd}); err == nil {
			t.Errorf("%q must be refused", cmd)
		}
	}
}
