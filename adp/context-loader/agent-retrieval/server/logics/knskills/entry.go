// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knskills

import (
	"path"
	"strings"
)

// shellMetaCharacters would change the meaning of a command line if a shell parsed it. A legacy
// entry_shell holding any of them outside quotes is not translated; see translateLegacyEntryShell.
const shellMetaCharacters = ";&|<>`$(){}*?!#~\n\r\\"

// translateLegacyEntryShell turns the deprecated "python <script> [args]" spelling (also python3,
// and a bare "<script>.py [args]") into the entry_point and arguments the execution factory takes.
//
// It translates only what it reads unambiguously. A command it cannot read as "python script args"
// (a Shell or Bash entry, shell syntax, an unterminated quote) reports ok=false and the caller
// sends it to the factory unchanged as entry_shell, so the rejection comes from the factory, the
// one place that owns the contract and its error messages, rather than from a second copy of it
// here.
func translateLegacyEntryShell(command string) (entry string, args []string, ok bool) {
	tokens, ok := splitLegacyCommand(command)
	if !ok || len(tokens) == 0 {
		return "", nil, false
	}
	if base := path.Base(tokens[0]); base == "python" || base == "python3" {
		tokens = tokens[1:]
		if len(tokens) == 0 {
			return "", nil, false
		}
	}
	if !strings.HasSuffix(strings.ToLower(tokens[0]), ".py") {
		return "", nil, false
	}
	return tokens[0], tokens[1:], true
}

// splitLegacyCommand splits on whitespace honouring single and double quotes. Nothing is run
// through a shell, so any shell metacharacter outside quotes makes the command untranslatable.
func splitLegacyCommand(command string) ([]string, bool) {
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
			return nil, false
		default:
			cur.WriteRune(r)
			inToken = true
		}
	}
	if quote != 0 {
		return nil, false
	}
	if inToken {
		tokens = append(tokens, cur.String())
	}
	return tokens, true
}
