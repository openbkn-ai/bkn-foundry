// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package cypher compiles a read-only subset of openCypher 9 into a single
// SQL statement executed through vega-backend's Raw Query interface.
//
// This file owns the first stage only: turning query text into a parse tree.
// The grammar accepts the whole language, so a tree here means the text is
// syntactically valid Cypher, not that the subset admits it. Deciding what is
// in the subset happens later, while walking the tree, so a rejection can say
// which construct is unsupported and why instead of surfacing a parser error.
package cypher

import (
	"fmt"
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/cypher/parsing"
)

// SyntaxError is one position-tagged message from the lexer or the parser.
type SyntaxError struct {
	Line   int
	Column int
	Msg    string
}

func (e SyntaxError) Error() string {
	return fmt.Sprintf("line %d, column %d: %s", e.Line, e.Column, e.Msg)
}

// SyntaxErrors is every error collected from one parse, in source order.
type SyntaxErrors []SyntaxError

func (e SyntaxErrors) Error() string {
	parts := make([]string, 0, len(e))
	for _, one := range e {
		parts = append(parts, one.Error())
	}
	return strings.Join(parts, "; ")
}

// collector replaces the default ANTLR listeners, which write to stderr and
// let a failed parse look successful.
type collector struct {
	*antlr.DefaultErrorListener
	lines []string
	errs  SyntaxErrors
}

func (c *collector) SyntaxError(_ antlr.Recognizer, _ any, line, column int,
	msg string, _ antlr.RecognitionException) {
	if hint := c.hintAt(line, column); hint != "" {
		msg += "; " + hint
	}
	c.errs = append(c.errs, SyntaxError{Line: line, Column: column, Msg: msg})
}

// hintAt names the standard spelling of what is written at an error, for
// spellings a query author brings from SQL. They stay errors: openCypher has
// neither, and the hint says what to write instead. != never reaches here:
// it is read as <> before parsing.
func (c *collector) hintAt(line, column int) string {
	if line < 1 || line > len(c.lines) {
		return ""
	}
	runes := []rune(c.lines[line-1])
	if column < 0 || column > len(runes) {
		return ""
	}
	rest := strings.TrimLeft(string(runes[column:]), " \t")
	switch {
	case len(rest) >= 5 && strings.EqualFold(rest[:5], "GROUP"):
		return "openCypher has no GROUP BY: aggregating in RETURN groups by the other columns RETURN lists, " +
			"as in RETURN n.property, count(*)"
	case startsWithWord(rest, "LIKE"):
		return "openCypher has no LIKE: match a substring with CONTAINS, STARTS WITH or ENDS WITH, " +
			"as in n.property CONTAINS 'value', where % and _ are plain characters"
	default:
		return ""
	}
}

// startsWithWord reports whether text starts with word, in any case, as a
// whole word rather than the start of a longer name such as likes.
func startsWithWord(text, word string) bool {
	if len(text) < len(word) || !strings.EqualFold(text[:len(word)], word) {
		return false
	}
	if len(text) == len(word) {
		return true
	}
	next := text[len(word)]
	return next != '_' && (next < '0' || next > '9') && (next < 'a' || next > 'z') && (next < 'A' || next > 'Z')
}

// Parse turns query text into a parse tree. A non-nil error is always of type
// SyntaxErrors and carries every position the parser complained about, so a
// caller can report them all rather than only the first.
func Parse(query string) (parsing.IOC_CypherContext, error) {
	c := &collector{lines: strings.Split(query, "\n")}

	lexer := parsing.NewCypherLexer(&writtenStream{
		InputStream: antlr.NewInputStream(notEqualAsStandard(query)),
		written:     []rune(query),
	})
	lexer.RemoveErrorListeners()
	lexer.AddErrorListener(c)

	p := parsing.NewCypherParser(antlr.NewCommonTokenStream(lexer, antlr.TokenDefaultChannel))
	p.RemoveErrorListeners()
	p.AddErrorListener(c)

	tree := p.OC_Cypher()
	if len(c.errs) > 0 {
		return nil, c.errs
	}
	return tree, nil
}

// writtenStream is what the lexer reads, != already read as <>, together
// with the query as it was written. Tokens keep the text they are read as;
// sourceText takes the written text, so a column named after the query reads
// as the caller wrote it. Both have the same length, so every position holds.
type writtenStream struct {
	*antlr.InputStream
	written []rune
}

// writtenText is the query as written between two positions.
func (s *writtenStream) writtenText(start, stop int) string {
	if start < 0 || stop >= len(s.written) || start > stop {
		return s.GetTextFromInterval(antlr.NewInterval(start, stop))
	}
	return string(s.written[start : stop+1])
}

// notEqualAsStandard reads != as <>, the not-equal openCypher spells, so a
// query written the way most languages write it runs as written. Only the
// operator is rewritten: != inside a string, a quoted name or a comment is
// left alone. Both spellings are two characters, so every position an error
// reports still points into the query as written.
func notEqualAsStandard(query string) string {
	if !strings.Contains(query, "!=") {
		return query
	}
	out := []byte(query)
	for i := 0; i < len(out); i++ {
		switch c := out[i]; {
		case c == '\'' || c == '"':
			// A string ends at its unescaped closing quote.
			for i++; i < len(out) && out[i] != c; i++ {
				if out[i] == '\\' {
					i++
				}
			}
		case c == '`':
			// A quoted name doubles a backtick to hold one.
			for i++; i < len(out); i++ {
				if out[i] == '`' {
					if i+1 < len(out) && out[i+1] == '`' {
						i++
						continue
					}
					break
				}
			}
		case c == '/' && i+1 < len(out) && out[i+1] == '/':
			for i += 2; i < len(out) && out[i] != '\n'; i++ {
			}
		case c == '/' && i+1 < len(out) && out[i+1] == '*':
			for i += 2; i+1 < len(out) && (out[i] != '*' || out[i+1] != '/'); i++ {
			}
			i++
		case c == '!' && i+1 < len(out) && out[i+1] == '=':
			out[i], out[i+1] = '<', '>'
			i++
		}
	}
	return string(out)
}
