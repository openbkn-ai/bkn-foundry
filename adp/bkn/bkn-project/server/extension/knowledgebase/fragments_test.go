// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledgebase

import (
	"errors"
	"testing"
)

func TestNativeSampleFragmentBoundaries(t *testing.T) {
	for _, c := range []struct {
		name, text string
		spans      []Span
		want       []string
	}{
		{"contract", "订单甲😀\n\né乙", []Span{{"p1b1", 10, 14}, {"p1b2", 30, 33}}, []string{"订单甲😀", "é乙"}},
		{"patent", "Part A\n\nMaterial B", []Span{{"p1b1", 5, 11}, {"p2b1", 90, 100}}, []string{"Part A", "Material B"}},
		{"same block gaps", "AB", []Span{{"p1b1", 2, 3}, {"p1b1", 9, 10}}, []string{"A", "B"}},
		{"native trailing newlines", "A\n\n\n\nB", []Span{{"p1b1", 2, 5}, {"p1b2", 9, 10}}, []string{"A\n\n", "B"}},
		{"repeated block", "A\n\nB\n\nC", []Span{{"p1b1", 1, 2}, {"p1b2", 4, 5}, {"p1b1", 8, 9}}, []string{"A", "B", "C"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := SplitSampleText(SampleChunk{Text: c.text, Spans: c.spans})
			if err != nil || len(got) != len(c.want) {
				t.Fatal("native layout rejected", err)
			}
			for i, want := range c.want {
				if got[i].Text != want || got[i].Span != c.spans[i] {
					t.Fatal("generated separator became evidence or source coordinates changed")
				}
			}
		})
	}
}

func TestNativeSampleFragmentLayoutRejectsUnlocatedOrAlteredText(t *testing.T) {
	for _, text := range []string{"AB", "A B", "A\nB", "A\r\nB", "A\n\nBX", "A\n\n", "\xff\n\nB"} {
		_, err := SplitSampleText(SampleChunk{Text: text, Spans: []Span{{"b1", 0, 1}, {"b2", 3, 4}}})
		if !errors.Is(err, ErrSampleSpanLayout) {
			t.Fatal("altered/unlocated text accepted")
		}
	}
	if _, err := SplitSampleText(SampleChunk{Text: "A"}); !errors.Is(err, ErrSampleSpanLayout) {
		t.Fatal("nonempty text without native intervals accepted")
	}
}
