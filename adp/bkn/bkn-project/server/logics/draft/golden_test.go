// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// goldenCase is a synthetic catalog: tables as vega-backend reads them, and what counting them
// answers. Cases cover what customers' catalogs do and the sample does not; each has its drafted
// items in <name>.draft.json. UPDATE_GOLDEN=1 rewrites those, so a change to the rules shows in
// review as a change to what they draft.
type goldenCase struct {
	Description string                  `json:"description"`
	Tables      []*interfaces.VegaTable `json:"tables"`
	Counts      struct {
		// Keys are rows, distinct and nulls by "table" or "table.field".
		Keys map[string][3]int64 `json:"keys"`
		// Joins are rows, nulls, orphans and distinct source values by "table.field".
		Joins  map[string][4]int64                    `json:"joins"`
		Codes  map[string][]interfaces.DraftEnumValue `json:"codes"`
		Failed []string                               `json:"failed"`
	} `json:"counts"`
}

// goldenCounter answers only what the case lists: a count drafting asks for that the case does not
// list is recorded, and fails the case, so no default can become a golden expectation.
type goldenCounter struct {
	c        *goldenCase
	unlisted []string
}

func (g *goldenCounter) failed(names ...string) bool {
	for _, name := range names {
		if slices.Contains(g.c.Counts.Failed, name) {
			return true
		}
	}
	return false
}

func (g *goldenCounter) keyStats(_ context.Context, table *interfaces.VegaTable, field string) (int64, int64, int64, error) {
	if g.failed(table.ID, table.ID+"."+field) {
		return 0, 0, 0, errors.New("timeout")
	}
	s, ok := g.c.Counts.Keys[table.ID+"."+field]
	if !ok {
		g.unlisted = append(g.unlisted, "keys "+table.ID+"."+field)
		return 0, 0, 0, errors.New("unlisted")
	}
	return s[0], s[1], s[2], nil
}

func (g *goldenCounter) keyTupleStats(ctx context.Context, table *interfaces.VegaTable,
	fields []string) (int64, int64, int64, error) {
	return g.keyStats(ctx, table, strings.Join(fields, "+"))
}

func (g *goldenCounter) joinStats(_ context.Context, source *interfaces.VegaTable, field string,
	_ *interfaces.VegaTable, _ string) (int64, int64, int64, int64, error) {
	if g.failed(source.ID, source.ID+"."+field) {
		return 0, 0, 0, 0, errors.New("timeout")
	}
	s, ok := g.c.Counts.Joins[source.ID+"."+field]
	if !ok {
		g.unlisted = append(g.unlisted, "joins "+source.ID+"."+field)
		return 0, 0, 0, 0, errors.New("unlisted")
	}
	return s[0], s[1], s[2], s[3], nil
}

func (g *goldenCounter) codeValues(_ context.Context, table *interfaces.VegaTable, _ string, _ string,
	_ int64) ([]interfaces.DraftEnumValue, error) {
	if g.failed(table.ID) {
		return nil, errors.New("timeout")
	}
	values, ok := g.c.Counts.Codes[table.ID]
	if !ok {
		g.unlisted = append(g.unlisted, "codes "+table.ID)
		return nil, errors.New("unlisted")
	}
	return values, nil
}

// draftGolden drafts the case's tables, in the order given, as JSON, and lists the counts drafting
// asked for that the case does not list.
func draftGolden(c *goldenCase, tables []*interfaces.VegaTable) ([]byte, []string, error) {
	clones := make([]*interfaces.VegaTable, 0, len(tables))
	for _, t := range tables {
		clone := *t
		clones = append(clones, &clone)
	}
	counter := &goldenCounter{c: c}
	items, err := newDrafter(testSetting, counter).draft(context.Background(), clones, nil)
	if err != nil {
		return nil, nil, err
	}
	drafted, err := json.MarshalIndent(items, "", "  ")
	return drafted, counter.unlisted, err
}

func Test_GoldenDrafts(t *testing.T) {
	Convey("Test drafting synthetic catalogs against their golden drafts\n", t, func() {
		inputs, err := filepath.Glob("testdata/golden/*.input.json")
		So(err, ShouldBeNil)
		So(inputs, ShouldNotBeEmpty)
		update := os.Getenv("UPDATE_GOLDEN") == "1"

		for _, input := range inputs {
			name := strings.TrimSuffix(filepath.Base(input), ".input.json")
			Convey(name+"\n", func() {
				data, err := os.ReadFile(input)
				So(err, ShouldBeNil)
				// Input keys are Go field names; a misspelled one would be dropped unseen.
				var c goldenCase
				dec := json.NewDecoder(bytes.NewReader(data))
				dec.DisallowUnknownFields()
				So(dec.Decode(&c), ShouldBeNil)

				drafted, unlisted, err := draftGolden(&c, c.Tables)
				So(err, ShouldBeNil)
				So(unlisted, ShouldBeEmpty)
				// The same tables listed in another order draft the same, byte for byte.
				reversed := slices.Clone(c.Tables)
				slices.Reverse(reversed)
				again, _, err := draftGolden(&c, reversed)
				So(err, ShouldBeNil)
				So(string(again), ShouldEqual, string(drafted))

				golden := filepath.Join("testdata/golden", name+".draft.json")
				if update {
					So(os.WriteFile(golden, append(drafted, '\n'), 0o644), ShouldBeNil)
				}
				want, err := os.ReadFile(golden)
				if errors.Is(err, os.ErrNotExist) {
					So("missing "+golden+"; run with UPDATE_GOLDEN=1", ShouldBeEmpty)
				}
				So(err, ShouldBeNil)
				So(string(drafted), ShouldEqual, string(bytes.TrimSuffix(want, []byte("\n"))))
			})
		}
	})
}
