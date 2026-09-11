// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"bufio"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/antlr4-go/antlr/v4"

	"bkn-backend/logics/cypher/parsing"
)

// The corpus in testdata carries one query for every rule of the openCypher
// grammar. These tests state the property that corpus exists for: the compiler
// has an answer for the whole language, not only for the part it implements.

func grammarCorpus(t *testing.T) []string {
	t.Helper()

	file, err := os.Open("testdata/grammar_corpus.cypher")
	if err != nil {
		t.Fatalf("open corpus: %v", err)
	}
	defer func() { _ = file.Close() }()

	var queries []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		queries = append(queries, line)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	return queries
}

// Every rule of the grammar has to be reached by something in the corpus.
// A rule nothing reaches is a construct nobody has ever handed the compiler,
// which is where an unhandled shape hides -- the whole point of keeping a
// corpus rather than only the cases the subset accepts.
func TestCorpusReachesEveryGrammarRule(t *testing.T) {
	lexer := parsing.NewCypherLexer(antlr.NewInputStream(""))
	ruleNames := parsing.NewCypherParser(antlr.NewCommonTokenStream(lexer, antlr.TokenDefaultChannel)).GetRuleNames()

	reached := make(map[int]bool, len(ruleNames))
	for _, query := range grammarCorpus(t) {
		tree, err := Parse(query)
		if err != nil {
			// A query the grammar itself rejects reaches no rule; the corpus
			// keeps a few on purpose, for the syntax-error path.
			continue
		}
		markRules(tree, reached)
	}

	var missing []string
	for index, name := range ruleNames {
		if !reached[index] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("%d of %d grammar rules are not reached by the corpus: %s",
			len(missing), len(ruleNames), strings.Join(missing, ", "))
	}
}

func markRules(tree antlr.Tree, reached map[int]bool) {
	if ctx, ok := tree.(antlr.ParserRuleContext); ok {
		reached[ctx.GetRuleIndex()] = true
	}
	for i := 0; i < tree.GetChildCount(); i++ {
		markRules(tree.GetChild(i), reached)
	}
}

// Whatever the corpus contains, the answer is a compiled query or a refusal
// that carries a position -- never a panic, and never an error of a type the
// service does not know how to turn into a status.
func TestCorpusIsAlwaysAnsweredOrRefusedByName(t *testing.T) {
	for _, query := range grammarCorpus(t) {
		t.Run(truncate(query), func(t *testing.T) {
			tree, err := Parse(query)
			if err != nil {
				var syntaxErrors SyntaxErrors
				if !asSyntaxErrors(err, &syntaxErrors) {
					t.Fatalf("parse error of type %T, want SyntaxErrors: %v", err, err)
				}
				if len(syntaxErrors) == 0 {
					t.Fatal("a parse failure reported no error")
				}
				return
			}

			analyzed, err := Analyze(tree)
			if err == nil {
				if analyzed == nil {
					t.Fatal("analyze returned neither a query nor an error")
				}
				return
			}
			unsupported, ok := err.(*Unsupported)
			if !ok {
				t.Fatalf("analyze error of type %T, want *Unsupported: %v", err, err)
			}
			if unsupported.Feature == "" {
				t.Fatalf("a refusal that does not name the construct: %v", err)
			}
			if unsupported.Pos.Line == 0 {
				t.Fatalf("a refusal without a position: %v", err)
			}
		})
	}
}

func asSyntaxErrors(err error, target *SyntaxErrors) bool {
	errs, ok := err.(SyntaxErrors)
	if ok {
		*target = errs
	}
	return ok
}

func truncate(query string) string {
	if len(query) <= 60 {
		return query
	}
	return query[:60]
}
