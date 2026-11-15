package skill

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
)

// A Skill runs in the sandbox as a Python script, and only that. The sandbox the platform ships
// has a Python runtime for Skills; a Shell or Bash entry could be registered and published but
// never ran, which is what #729 reported. The execution contract therefore names a Python
// script of the package plus its argv. The server never builds a command line from the caller's
// free text; entry and arguments are quoted individually, and the characters the sandbox's command
// normaliser would treat as separators are refused up front (see commandSeparators).

const (
	// skillRuntimePython is the one runtime Skills execute with.
	skillRuntimePython = "python"
	// skillPythonInterpreter is the interpreter the sandbox image provides.
	skillPythonInterpreter = "python3"
	skillEntryExtension    = ".py"
	// shellMetaCharacters would change the meaning of a command line if a shell parsed it.
	shellMetaCharacters = ";&|<>`$(){}*?!#~\n\r\\"
	// commandSeparators are the characters the sandbox's normalize_shell_code splits a command on
	// without honouring quotes, so quoting cannot make them safe inside an argv element.
	commandSeparators = ";&|\n\r"
)

func entryInvalid(ctx context.Context, reason string) error {
	return errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtSkillEntryInvalid, reason, reason)
}

// resolveSkillEntry turns an execute request into a Python script path and its argv, and checks the
// script is a file of the Skill. manifest holds the rel_path of every file in the package.
func resolveSkillEntry(ctx context.Context, req *interfaces.ExecuteSkillReq, manifest []string) (entry string, args []string, err error) {
	switch {
	case req.EntryPoint != "" && req.EntryShell != "":
		return "", nil, entryInvalid(ctx, "send entry_point and arguments, not entry_shell as well")
	case req.EntryPoint != "":
		entry, args = req.EntryPoint, req.Arguments
	case req.EntryShell != "":
		// Deprecated spelling, kept so existing callers that send "python main.py" still work.
		// It is split into argv here; anything a shell would interpret is refused instead.
		entry, args, err = parseLegacyEntryShell(ctx, req.EntryShell)
		if err != nil {
			return "", nil, err
		}
	default:
		return "", nil, entryInvalid(ctx, "entry_point is required, for example scripts/main.py")
	}
	for _, arg := range args {
		if strings.ContainsRune(arg, 0) {
			return "", nil, entryInvalid(ctx, "arguments must not contain NUL")
		}
	}
	for _, value := range append([]string{entry}, args...) {
		if strings.ContainsAny(value, commandSeparators) {
			return "", nil, entryInvalid(ctx, "entry_point and arguments must not contain the characters ; & | or a line break (the sandbox splits its command line on them even inside quotes) / entry_point 和 arguments 不允许包含 ; & | 或换行符（沙箱会在这些字符处拆分命令行，引号也不起作用）")
		}
	}
	entry, err = validateEntryPath(ctx, entry, manifest)
	if err != nil {
		return "", nil, err
	}
	return entry, args, nil
}

// parseLegacyEntryShell accepts "python main.py --flag value" (also python3, and a bare
// "main.py"), with single or double quotes for arguments that contain spaces.
func parseLegacyEntryShell(ctx context.Context, command string) (entry string, args []string, err error) {
	tokens, err := splitCommand(ctx, command)
	if err != nil {
		return "", nil, err
	}
	if len(tokens) == 0 {
		return "", nil, entryInvalid(ctx, "entry_shell is empty")
	}
	first := path.Base(tokens[0])
	if first == "python" || first == "python3" {
		tokens = tokens[1:]
		if len(tokens) == 0 {
			return "", nil, entryInvalid(ctx, "no script after python; use entry_point, for example scripts/main.py")
		}
	} else if !strings.HasSuffix(strings.ToLower(tokens[0]), skillEntryExtension) {
		return "", nil, entryInvalid(ctx, fmt.Sprintf(
			"%q is not a Python script; Shell and Bash entries such as \"bash run.sh\" are not supported, rewrite the Skill as a Python script and call it with entry_point", tokens[0]))
	}
	return tokens[0], tokens[1:], nil
}

// splitCommand splits on whitespace honouring quotes, and refuses shell metacharacters outside
// quotes. Nothing here is executed through a shell.
func splitCommand(ctx context.Context, command string) ([]string, error) {
	var tokens []string
	var cur strings.Builder
	inToken := false
	var quote rune
	for _, r := range command {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			inToken = true
		case r == ' ' || r == '\t':
			if inToken {
				tokens = append(tokens, cur.String())
				cur.Reset()
				inToken = false
			}
		case strings.ContainsRune(shellMetaCharacters, r):
			return nil, entryInvalid(ctx, fmt.Sprintf(
				"shell syntax %q is not supported; send entry_point and arguments instead", string(r)))
		default:
			cur.WriteRune(r)
			inToken = true
		}
	}
	if quote != 0 {
		return nil, entryInvalid(ctx, "unterminated quote in entry_shell")
	}
	if inToken {
		tokens = append(tokens, cur.String())
	}
	return tokens, nil
}

// validateEntryPath requires a relative path without traversal to a .py file in the package.
func validateEntryPath(ctx context.Context, entry string, manifest []string) (string, error) {
	if entry == "" || strings.ContainsAny(entry, "\x00\\") {
		return "", entryInvalid(ctx, "entry_point is not a valid relative path")
	}
	if strings.HasPrefix(entry, "/") || path.IsAbs(entry) {
		return "", entryInvalid(ctx, "entry_point must be relative to the Skill package, not absolute")
	}
	for _, segment := range strings.Split(entry, "/") {
		if segment == ".." {
			return "", entryInvalid(ctx, "entry_point must not contain \"..\"")
		}
	}
	clean := path.Clean(entry)
	if !strings.HasSuffix(strings.ToLower(clean), skillEntryExtension) {
		return "", entryInvalid(ctx, fmt.Sprintf(
			"%q is not a Python script; Skill execution supports .py entries only", entry))
	}
	for _, rel := range manifest {
		if path.Clean(rel) == clean {
			return clean, nil
		}
	}
	return "", entryInvalid(ctx, fmt.Sprintf("%q is not a file of this Skill", entry))
}

// quoteArg single-quotes one argument so the shell reads it back as one argv element. Quoting alone
// is not enough for the sandbox, which splits on ; & | and line breaks before the shell sees the
// quotes; resolveSkillEntry therefore refuses those characters before this is reached.
func quoteArg(arg string) string {
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}

// pythonCommand is the command line the sandbox runs: the interpreter, the script and its argv.
func pythonCommand(entry string, args []string) string {
	parts := make([]string, 0, len(args)+2)
	parts = append(parts, skillPythonInterpreter, quoteArg(entry))
	for _, arg := range args {
		parts = append(parts, quoteArg(arg))
	}
	return strings.Join(parts, " ")
}

// skillRuntimeOf reports what the platform can run for a package, from its file manifest. A package
// without a Python script is marked not executable, with a hint for migrating it.
func skillRuntimeOf(files []*interfaces.SkillFileSummary) *interfaces.SkillRuntime {
	runtime := &interfaces.SkillRuntime{Runtime: skillRuntimePython}
	hasShell := false
	for _, file := range files {
		if file == nil {
			continue
		}
		lower := strings.ToLower(file.RelPath)
		switch {
		case strings.HasSuffix(lower, skillEntryExtension):
			runtime.EntryPoints = append(runtime.EntryPoints, file.RelPath)
		case strings.HasSuffix(lower, ".sh"), strings.HasSuffix(lower, ".bash"):
			hasShell = true
		}
	}
	runtime.Executable = len(runtime.EntryPoints) > 0
	switch {
	case runtime.Executable:
	case hasShell:
		runtime.Reason = "the package has Shell/Bash scripts only; Skill execution supports Python scripts, rewrite them in Python (standard library) to make the Skill executable"
	default:
		runtime.Reason = "the package has no Python script; it can be read but not executed"
	}
	return runtime
}

// manifestPaths lists the rel_path of the files a Skill version holds.
func manifestPaths(files []*model.SkillFileIndexDB) []string {
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.RelPath)
	}
	return paths
}
