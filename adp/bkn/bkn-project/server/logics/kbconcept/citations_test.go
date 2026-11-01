// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbconcept

import (
	"context"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"testing"
)

func TestCitationChecksUnicodeVersionAndOneRealSpan(t *testing.T) {
	input, err := LoadSamples(context.Background(), &sampleReader{pages: []kb.SamplePage{{Entries: []kb.SampleChunk{chunk()}}}}, selected(), "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	quote := input.Request.Samples[0].Citation
	quote.Start = 12
	quote.End = 14
	quote.Quote = "甲😀"
	clean, err := ValidateCitations(input, []kb.Citation{quote})
	if err != nil {
		t.Fatal(err)
	}
	if len(clean) != 1 || clean[0].Quote != "" || clean[0].Start != 12 || clean[0].End != 14 {
		t.Fatal("disabled original-text scope must retain location only")
	}
	input.Files[0].IncludeOriginal = true
	clean, err = ValidateCitations(input, []kb.Citation{quote})
	if err != nil || clean[0].Quote != "甲😀" {
		t.Fatal("enabled original text must retain the verified quote", err)
	}
	for _, name := range []string{"wrong_quote", "wrong_version", "heldout", "wrong_block", "wrong_offset", "multi_block", "empty"} {
		t.Run(name, func(t *testing.T) {
			q := quote
			switch name {
			case "wrong_quote":
				q.Quote = "另一句"
			case "wrong_version":
				q.ContentVersionID = "9"
			case "heldout":
				q.KnowledgeBaseID = "2"
				q.FileID = "51"
				q.ContentVersionID = "20"
			case "wrong_block":
				q.BlockID = "unknown"
			case "wrong_offset":
				q.Start = 13
			case "multi_block":
				q.Start = 10
				q.End = 33
				q.Quote = "订单甲😀é乙"
			case "empty":
				q.Quote = ""
			}
			if _, err := ValidateCitations(input, []kb.Citation{q}); err == nil {
				t.Fatal("unverified quote accepted")
			}
		})
	}
}
func TestCitationRulesAreIndependentOfBusinessDomain(t *testing.T) {
	for _, text := range []string{"合同 Q-19 的交期为 2030 年。", "专利 PX-8 包含导流板。", "维修工单 M-3 在周一关闭。"} {
		s := selected()
		r := chunk()
		r.Text = text
		r.Spans = []kb.Span{{BlockID: "p2b1", Start: 0, End: len([]rune(text))}}
		input, err := LoadSamples(context.Background(), &sampleReader{pages: []kb.SamplePage{{Entries: []kb.SampleChunk{r}}}}, s, "zh-CN")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ValidateCitations(input, []kb.Citation{input.Request.Samples[0].Citation}); err != nil {
			t.Fatal(err)
		}
	}
}
