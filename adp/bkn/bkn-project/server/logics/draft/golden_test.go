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

func (c *goldenCase) counter() *fakeCounter {
	f := &fakeCounter{
		keys: map[string][3]int64{}, joins: map[joinKey][4]int64{},
		codes: c.Counts.Codes, failed: map[string]bool{},
	}
	for k, v := range c.Counts.Keys {
		f.keys[k] = v
	}
	for k, v := range c.Counts.Joins {
		table, field, _ := strings.Cut(k, ".")
		f.joins[joinKey{table, field}] = v
	}
	for _, k := range c.Counts.Failed {
		f.failed[k] = true
	}
	return f
}

// draftGolden drafts the case's tables, in the order given, as JSON.
func draftGolden(c *goldenCase, tables []*interfaces.VegaTable) ([]byte, error) {
	clones := make([]*interfaces.VegaTable, 0, len(tables))
	for _, t := range tables {
		clone := *t
		clones = append(clones, &clone)
	}
	items, err := newDrafter(testSetting, c.counter()).draft(context.Background(), clones, nil)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(items, "", "  ")
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
				var c goldenCase
				So(json.Unmarshal(data, &c), ShouldBeNil)

				drafted, err := draftGolden(&c, c.Tables)
				So(err, ShouldBeNil)
				// The same tables listed in another order draft the same, byte for byte.
				reversed := slices.Clone(c.Tables)
				slices.Reverse(reversed)
				again, err := draftGolden(&c, reversed)
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
