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
// neither, and the hint says what to write instead.
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
	case strings.HasPrefix(rest, "!="):
		return "openCypher writes not-equal as <>, as in n.property <> 'value'; != is not supported"
	case len(rest) >= 5 && strings.EqualFold(rest[:5], "GROUP"):
		return "openCypher has no GROUP BY: aggregating in RETURN groups by the other columns RETURN lists, " +
			"as in RETURN n.property, count(*)"
	default:
		return ""
	}
}

// Parse turns query text into a parse tree. A non-nil error is always of type
// SyntaxErrors and carries every position the parser complained about, so a
// caller can report them all rather than only the first.
func Parse(query string) (parsing.IOC_CypherContext, error) {
	c := &collector{lines: strings.Split(query, "\n")}

	lexer := parsing.NewCypherLexer(antlr.NewInputStream(query))
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
