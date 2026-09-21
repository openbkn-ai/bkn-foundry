// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

// Package entity holds the domain entities of bkn-eval. The dataset contract
// is a versioned set of cases, each with the facts a correct answer must state
// and the tool paths that count as a correct way to get there. A future
// bkn-eval service stores the same entities, so nothing here may depend on how
// a dataset is read or how a run is executed.
package entity

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Solvability says which entry points can answer a case. A case that only the
// full entry can solve is still scored on the compact entry: there, the right
// behavior is to state the boundary instead of answering.
type Solvability string

const (
	SolvableOnBoth     Solvability = "both"
	SolvableOnFullOnly Solvability = "full_only"
	SolvableWithoutMCP Solvability = "no_mcp"
)

// Split keeps held-out cases away from anything that is tuned against the
// dataset, such as tool descriptions, instructions or defaults.
type Split string

const (
	SplitDev     Split = "dev"
	SplitHoldout Split = "holdout"
)

// MatchType is how a fact is checked against the final answer.
type MatchType string

const (
	MatchContains MatchType = "contains"
	MatchRegex    MatchType = "regex"
	MatchNumber   MatchType = "number"
)

// Dataset is one versioned set of cases against one knowledge network.
type Dataset struct {
	DatasetID   string  `json:"dataset_id"`
	Version     string  `json:"version"`
	Description string  `json:"description,omitempty"`
	Network     Network `json:"network"`
	Cases       []Case  `json:"cases"`
}

// Network names the fixture a dataset runs against. Only public fixtures are
// allowed: this repository is public, so no customer network may appear here.
type Network struct {
	Name   string `json:"name"`
	Source string `json:"source,omitempty"`
}

// Case is one question with its expected facts and acceptable tool paths.
type Case struct {
	CaseID          string      `json:"case_id"`
	Locale          string      `json:"locale"`
	Split           Split       `json:"split"`
	Solvability     Solvability `json:"solvability"`
	Input           Input       `json:"input"`
	Facts           []Fact      `json:"facts"`
	AcceptablePaths [][]string  `json:"acceptable_paths"`
	Tags            []string    `json:"tags,omitempty"`
}

// Input mirrors the bkn-sdk eval-set case input so existing cases map across.
type Input struct {
	UserMessage string `json:"user_message"`
}

// Fact is one thing a correct answer has to state.
type Fact struct {
	ID        string    `json:"id"`
	Statement string    `json:"statement"`
	Match     FactMatch `json:"match"`
}

// FactMatch checks a fact against the answer text. Value is a string for
// contains and regex, and a number for number.
type FactMatch struct {
	Type      MatchType       `json:"type"`
	Value     json.RawMessage `json:"value"`
	Tolerance float64         `json:"tolerance,omitempty"`
}

var (
	idPattern       = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	supportedLocale = map[string]bool{"zh-CN": true, "en-US": true}
)

// Validate reports every problem it finds, not only the first one, so a
// dataset author can fix a file in one pass.
func (ds *Dataset) Validate() error {
	var problems []error
	add := func(format string, args ...any) { problems = append(problems, fmt.Errorf(format, args...)) }

	if !idPattern.MatchString(ds.DatasetID) {
		add("dataset_id %q must be lowercase letters, digits and hyphens", ds.DatasetID)
	}
	if strings.TrimSpace(ds.Version) == "" {
		add("version is required")
	}
	if strings.TrimSpace(ds.Network.Name) == "" {
		add("network.name is required")
	}
	if len(ds.Cases) == 0 {
		add("at least one case is required")
	}
	seen := map[string]bool{}
	for i, c := range ds.Cases {
		where := fmt.Sprintf("cases[%d] (%s)", i, c.CaseID)
		if !idPattern.MatchString(c.CaseID) {
			add("%s: case_id must be lowercase letters, digits and hyphens", where)
		} else if seen[c.CaseID] {
			add("%s: duplicate case_id", where)
		}
		seen[c.CaseID] = true
		if !supportedLocale[c.Locale] {
			add("%s: locale %q is not supported", where, c.Locale)
		}
		if c.Split != SplitDev && c.Split != SplitHoldout {
			add("%s: split must be dev or holdout", where)
		}
		switch c.Solvability {
		case SolvableOnBoth, SolvableOnFullOnly:
			if len(c.AcceptablePaths) == 0 {
				add("%s: at least one acceptable path is required", where)
			}
		case SolvableWithoutMCP:
			if len(c.AcceptablePaths) != 0 {
				add("%s: a case solvable without MCP must not list tool paths", where)
			}
		default:
			add("%s: solvability must be both, full_only or no_mcp", where)
		}
		for j, path := range c.AcceptablePaths {
			if len(path) == 0 {
				add("%s: acceptable_paths[%d] is empty", where, j)
			}
			for _, tool := range path {
				if strings.TrimSpace(tool) == "" {
					add("%s: acceptable_paths[%d] has an empty tool name", where, j)
				}
			}
		}
		if strings.TrimSpace(c.Input.UserMessage) == "" {
			add("%s: input.user_message is required", where)
		}
		if len(c.Facts) == 0 {
			add("%s: at least one fact is required", where)
		}
		factIDs := map[string]bool{}
		for _, f := range c.Facts {
			if f.ID == "" || factIDs[f.ID] {
				add("%s: fact ids must be present and unique", where)
			}
			factIDs[f.ID] = true
			if err := f.Match.validate(); err != nil {
				add("%s: fact %s: %v", where, f.ID, err)
			}
		}
	}
	return errors.Join(problems...)
}

func (m FactMatch) validate() error {
	switch m.Type {
	case MatchContains, MatchRegex:
		var s string
		if err := json.Unmarshal(m.Value, &s); err != nil || s == "" {
			return fmt.Errorf("%s needs a non-empty string value", m.Type)
		}
		if m.Type == MatchRegex {
			if _, err := regexp.Compile(s); err != nil {
				return fmt.Errorf("invalid regex: %v", err)
			}
		}
	case MatchNumber:
		var n float64
		if err := json.Unmarshal(m.Value, &n); err != nil {
			return errors.New("number needs a numeric value")
		}
		if m.Tolerance < 0 {
			return errors.New("tolerance must not be negative")
		}
	default:
		return fmt.Errorf("match type must be contains, regex or number, got %q", m.Type)
	}
	return nil
}
