// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestTCKM19Coverage reports how much of the openCypher M19 TCK the analyzer
// accepts. It reads the official archive from CYPHER_TCK_M19 and skips
// without it, so it never needs the network and never fails CI: the numbers
// are tracked, not enforced. It measures the query language only; whether
// an accepted query compiles against a model and returns the TCK's result is
// not checked here.
//
//	curl -o /tmp/tck-M19.zip https://s3.amazonaws.com/artifacts.opencypher.org/M19/tck-M19.zip
//	CYPHER_TCK_M19=/tmp/tck-M19.zip go test ./logics/cypher/ -run TestTCKM19Coverage -v
func TestTCKM19Coverage(t *testing.T) {
	path := os.Getenv("CYPHER_TCK_M19")
	if path == "" {
		t.Skip("set CYPHER_TCK_M19 to the path of tck-M19.zip to report coverage")
	}
	scenarios, err := readTCKScenarios(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := range scenarios {
		scenarios[i].outcome, scenarios[i].feature = analyzeTCKQuery(scenarios[i].query)
	}
	t.Log("\n" + tckReport(scenarios))
}

// tckM19SHA256 pins the archive the numbers refer to.
const tckM19SHA256 = "93d3ea6dcf386a64754bb561936a7035c3b429f81b9bc091b809061b4ea0d9bd"

type tckScenario struct {
	source   string // feature file, as tck/features/clauses/match/Match1.feature
	name     string
	query    string // empty when the scenario runs other than one query
	scope    string
	expected string // the error the TCK expects at compile time, if any
	outcome  string
	feature  string // what the analyzer refused
}

// Scopes of a scenario, from what its query is.
const (
	tckMatchFirst = "match-first read" // starts with MATCH, writes nothing: what run_cypher serves
	tckOtherRead  = "other read"       // RETURN, WITH, UNWIND or CALL first
	tckWrite      = "write"            // creates, merges, sets, removes or deletes
	tckMultiStep  = "multi-step"       // runs other than exactly one query
)

// Outcomes of analyzing a query.
const (
	tckAccepted   = "accepted"
	tckRefused    = "refused"
	tckParseError = "parse error"
	tckPanic      = "panic"
)

var (
	tckScenarioStart = regexp.MustCompile(`(?m)^  Scenario(?: Outline)?:`)
	tckBackground    = regexp.MustCompile(`(?ms)^  Background:.*?(?:^  Scenario)`)
	tckExamples      = regexp.MustCompile(`Examples:[^\n]*\n((?:[ \t]*\|[^\n]*\n?)+)`)
	tckQuery         = regexp.MustCompile(`(?s)(?:When|And) executing (?:query|control query):\s+"""(.*?)"""`)
	tckCompileError  = regexp.MustCompile(`Then (?:a|an) (\w+) should be raised at compile time:\s*(\w+)`)
	// Strings, quoted names and comments hold no keywords.
	tckNonCode   = regexp.MustCompile("(?s)'(?:\\\\.|''|[^'\\\\])*'|\"(?:\\\\.|\"\"|[^\"\\\\])*\"|`(?:``|[^`])*`|//[^\n]*|/\\*.*?\\*/")
	tckWrites    = regexp.MustCompile(`(?i)\b(?:CREATE|MERGE|DELETE|SET|REMOVE)\b`)
	tckMatchHead = regexp.MustCompile(`(?i)^\s*(?:OPTIONAL\s+)?MATCH\b`)
)

func readTCKScenarios(path string) ([]tckScenario, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != tckM19SHA256 {
		return nil, errors.New("the archive is not the pinned tck-M19.zip")
	}
	archive, err := zip.NewReader(strings.NewReader(string(raw)), int64(len(raw)))
	if err != nil {
		return nil, err
	}
	files := append([]*zip.File(nil), archive.File...)
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })

	var scenarios []tckScenario
	for _, file := range files {
		if !strings.HasSuffix(file.Name, ".feature") {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			return nil, err
		}
		content, err := io.ReadAll(reader)
		closeErr := reader.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
		scenarios = append(scenarios, tckFeatureScenarios(file.Name, string(content))...)
	}
	return scenarios, nil
}

// tckFeatureScenarios expands a feature file into one scenario per example
// row, with the background and the row's values filled in.
func tckFeatureScenarios(source, feature string) []tckScenario {
	background := ""
	if match := tckBackground.FindString(feature); match != "" {
		background = strings.TrimSuffix(match, "  Scenario")
	}
	starts := tckScenarioStart.FindAllStringIndex(feature, -1)
	var scenarios []tckScenario
	for i, start := range starts {
		end := len(feature)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		block := feature[start[0]:end]
		name := strings.TrimSpace(strings.SplitN(block, "\n", 2)[0])

		examples := []map[string]string{{}}
		if strings.HasPrefix(name, "Scenario Outline:") {
			examples = nil
			for _, table := range tckExamples.FindAllStringSubmatch(block, -1) {
				var header []string
				for _, line := range strings.Split(strings.TrimRight(table[1], "\n"), "\n") {
					cells := tckCells(line)
					if header == nil {
						header = cells
						continue
					}
					row := map[string]string{}
					for k, key := range header {
						if k < len(cells) {
							row[key] = cells[k]
						}
					}
					examples = append(examples, row)
				}
			}
		}
		for _, example := range examples {
			expanded := background + block
			for key, value := range example {
				expanded = strings.ReplaceAll(expanded, "<"+key+">", value)
			}
			scenarios = append(scenarios, tckClassify(source, name, expanded))
		}
	}
	return scenarios
}

func tckClassify(source, name, expanded string) tckScenario {
	scenario := tckScenario{source: source, name: name}
	if match := tckCompileError.FindStringSubmatch(expanded); match != nil {
		scenario.expected = match[1] + ": " + match[2]
	}
	queries := tckQuery.FindAllStringSubmatch(expanded, -1)
	if len(queries) != 1 {
		scenario.scope = tckMultiStep
		return scenario
	}
	scenario.query = strings.TrimSpace(queries[0][1])
	code := tckNonCode.ReplaceAllString(scenario.query, " ")
	switch {
	case tckWrites.MatchString(code):
		scenario.scope = tckWrite
	case tckMatchHead.MatchString(code):
		scenario.scope = tckMatchFirst
	default:
		scenario.scope = tckOtherRead
	}
	return scenario
}

// tckCells splits one row of a Gherkin table.
func tckCells(line string) []string {
	text := strings.TrimSpace(line)
	text = strings.TrimSuffix(strings.TrimPrefix(text, "|"), "|")
	var cells []string
	var current strings.Builder
	for i := 0; i < len(text); i++ {
		switch {
		case text[i] == '\\' && i+1 < len(text) && strings.ContainsRune(`\|n`, rune(text[i+1])):
			i++
			if text[i] == 'n' {
				current.WriteByte('\n')
			} else {
				current.WriteByte(text[i])
			}
		case text[i] == '|':
			cells = append(cells, strings.TrimSpace(current.String()))
			current.Reset()
		default:
			current.WriteByte(text[i])
		}
	}
	return append(cells, strings.TrimSpace(current.String()))
}

// analyzeTCKQuery parses and analyzes one query. A panic is reported as an
// outcome, since any input reaching the analyzer must be refused cleanly.
func analyzeTCKQuery(query string) (outcome, feature string) {
	if query == "" {
		return "", ""
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			outcome, feature = tckPanic, fmt.Sprint(recovered)
		}
	}()
	tree, err := Parse(query)
	if err != nil {
		return tckParseError, ""
	}
	if _, err := Analyze(tree); err != nil {
		var refused *Unsupported
		if errors.As(err, &refused) {
			return tckRefused, refused.Feature
		}
		return tckRefused, err.Error()
	}
	return tckAccepted, ""
}

func tckReport(scenarios []tckScenario) string {
	var out strings.Builder
	ratio := func(n, total int) string {
		if total == 0 {
			return "-"
		}
		return fmt.Sprintf("%d / %d (%.1f%%)", n, total, 100*float64(n)/float64(total))
	}

	// Scenarios expecting a compile-time error count apart: refusing them
	// is the right answer.
	byScope := map[string][2]int{}
	var expectingError, refusedAsExpected int
	var acceptedDespiteError []tckScenario
	for _, s := range scenarios {
		if s.expected != "" {
			if s.query == "" || s.scope == tckWrite {
				continue
			}
			expectingError++
			if s.outcome == tckAccepted {
				acceptedDespiteError = append(acceptedDespiteError, s)
			} else {
				refusedAsExpected++
			}
			continue
		}
		counts := byScope[s.scope]
		counts[1]++
		if s.outcome == tckAccepted {
			counts[0]++
		}
		byScope[s.scope] = counts
	}

	fmt.Fprintf(&out, "openCypher M19 TCK, %d expanded scenarios (archive sha256 %s)\n\n", len(scenarios), tckM19SHA256[:12])
	out.WriteString("| Scope | Accepted |\n|---|---|\n")
	var reads, readsAccepted int
	for _, scope := range []string{tckMatchFirst, tckOtherRead, tckWrite, tckMultiStep} {
		counts := byScope[scope]
		fmt.Fprintf(&out, "| %s | %s |\n", scope, ratio(counts[0], counts[1]))
		if scope == tckMatchFirst || scope == tckOtherRead {
			reads += counts[1]
			readsAccepted += counts[0]
		}
	}
	fmt.Fprintf(&out, "| all reads | %s |\n", ratio(readsAccepted, reads))
	fmt.Fprintf(&out, "| compile-time errors refused | %s |\n\n", ratio(refusedAsExpected, expectingError))

	// Match-first reads by directory, and what refused them.
	type tally struct{ accepted, total int }
	byDirectory := map[string]*tally{}
	reasons := map[string]int{}
	panics := 0
	for _, s := range scenarios {
		if s.scope != tckMatchFirst || s.expected != "" {
			continue
		}
		parts := strings.Split(s.source, "/")
		directory := s.source
		if len(parts) >= 4 {
			directory = parts[2] + "/" + parts[3]
		}
		if byDirectory[directory] == nil {
			byDirectory[directory] = &tally{}
		}
		byDirectory[directory].total++
		switch s.outcome {
		case tckAccepted:
			byDirectory[directory].accepted++
		case tckRefused:
			reasons[s.feature]++
		case tckParseError:
			reasons["(parse error)"]++
		case tckPanic:
			panics++
			reasons["(panic) "+s.feature]++
		}
	}
	directories := make([]string, 0, len(byDirectory))
	for directory := range byDirectory {
		directories = append(directories, directory)
	}
	sort.Slice(directories, func(i, j int) bool {
		a, b := byDirectory[directories[i]], byDirectory[directories[j]]
		if a.total != b.total {
			return a.total > b.total
		}
		return directories[i] < directories[j]
	})
	out.WriteString("Match-first reads by directory:\n\n| Directory | Accepted |\n|---|---|\n")
	for _, directory := range directories {
		fmt.Fprintf(&out, "| %s | %s |\n", directory, ratio(byDirectory[directory].accepted, byDirectory[directory].total))
	}

	type reason struct {
		feature string
		count   int
	}
	ranked := make([]reason, 0, len(reasons))
	for feature, count := range reasons {
		ranked = append(ranked, reason{feature, count})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].count != ranked[j].count {
			return ranked[i].count > ranked[j].count
		}
		return ranked[i].feature < ranked[j].feature
	})
	out.WriteString("\nWhat refuses match-first reads:\n\n| Refused construct | Scenarios |\n|---|---|\n")
	for _, r := range ranked {
		fmt.Fprintf(&out, "| %s | %d |\n", r.feature, r.count)
	}
	if panics > 0 {
		fmt.Fprintf(&out, "\n%d scenarios panicked in the analyzer.\n", panics)
	}

	if len(acceptedDespiteError) > 0 {
		out.WriteString("\nAccepted although the TCK expects a compile-time error:\n\n")
		for _, s := range acceptedDespiteError {
			fmt.Fprintf(&out, "- %s %s (%s): %s\n", strings.TrimPrefix(s.source, "tck/features/"), s.name, s.expected,
				strings.Join(strings.Fields(s.query), " "))
		}
	}
	return out.String()
}
