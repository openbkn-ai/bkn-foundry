// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package datasetvo

import (
	"encoding/json"
	"strings"
	"testing"
)

func validCase() Case {
	return Case{
		CaseID:          "one-hop",
		Locale:          "en-US",
		Split:           SplitDev,
		Solvability:     SolvableOnBoth,
		Input:           Input{UserMessage: "Which order does item 1 belong to?"},
		Facts:           []Fact{{ID: "f1", Statement: "order 7", Match: FactMatch{Type: MatchContains, Value: json.RawMessage(`"7"`)}}},
		AcceptablePaths: [][]string{{"query_instance_subgraph"}},
	}
}

func validDataset() Dataset {
	return Dataset{DatasetID: "probe", Version: "1", Network: Network{Name: "probe_net"}, Cases: []Case{validCase()}}
}

func TestValidateAcceptsAWellFormedDataset(t *testing.T) {
	ds := validDataset()
	if err := ds.Validate(); err != nil {
		t.Fatalf("expected valid dataset, got %v", err)
	}
}

func TestValidateReportsEveryProblem(t *testing.T) {
	ds := validDataset()
	second := validCase()
	second.Locale = "fr-FR"
	second.Split = "train"
	second.Facts = nil
	ds.Cases = append(ds.Cases, second)

	err := ds.Validate()
	if err == nil {
		t.Fatal("expected problems")
	}
	for _, want := range []string{"duplicate case_id", "locale \"fr-FR\"", "split must be", "at least one fact"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
}

func TestValidateSolvabilityAndPaths(t *testing.T) {
	cases := map[string]func(*Case){
		"no_mcp with tool paths":  func(c *Case) { c.Solvability = SolvableWithoutMCP },
		"both without tool paths": func(c *Case) { c.AcceptablePaths = nil },
		"empty path":              func(c *Case) { c.AcceptablePaths = [][]string{{}} },
		"unknown solvability":     func(c *Case) { c.Solvability = "sometimes" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			ds := validDataset()
			mutate(&ds.Cases[0])
			if err := ds.Validate(); err == nil {
				t.Fatal("expected a validation error")
			}
		})
	}

	ds := validDataset()
	ds.Cases[0].Solvability = SolvableWithoutMCP
	ds.Cases[0].AcceptablePaths = nil
	if err := ds.Validate(); err != nil {
		t.Fatalf("no_mcp without paths should be valid, got %v", err)
	}
}

func TestValidateFactMatches(t *testing.T) {
	cases := map[string]FactMatch{
		"contains needs a string": {Type: MatchContains, Value: json.RawMessage(`42`)},
		"regex must compile":      {Type: MatchRegex, Value: json.RawMessage(`"("`)},
		"number needs a number":   {Type: MatchNumber, Value: json.RawMessage(`"79"`)},
		"negative tolerance":      {Type: MatchNumber, Value: json.RawMessage(`79`), Tolerance: -1},
		"unknown type":            {Type: "fuzzy", Value: json.RawMessage(`"x"`)},
	}
	for name, match := range cases {
		t.Run(name, func(t *testing.T) {
			ds := validDataset()
			ds.Cases[0].Facts[0].Match = match
			if err := ds.Validate(); err == nil {
				t.Fatal("expected a validation error")
			}
		})
	}
}
