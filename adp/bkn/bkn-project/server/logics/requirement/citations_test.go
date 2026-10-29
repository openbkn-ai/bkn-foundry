// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"fmt"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// doc builds a document whose blocks are its paragraphs, as plain text is split.
func doc(id string, docType string, paragraphs ...string) *interfaces.RequirementDocument {
	d := &interfaces.RequirementDocument{ID: id, DocType: docType, Format: interfaces.REQUIREMENT_FORMAT_TEXT}
	for i, p := range paragraphs {
		if i > 0 {
			d.Content += "\n\n"
		}
		d.Content += p
	}
	start := 0
	for i, p := range paragraphs {
		n := len([]rune(p))
		d.Blocks = append(d.Blocks, interfaces.RequirementBlock{ID: fmt.Sprintf("b%d", i+1), Start: start, End: start + n})
		start += n + 2
	}
	return d
}

func Test_LocateQuote(t *testing.T) {
	Convey("Test locateQuote\n", t, func() {
		d := doc("d1", interfaces.REQUIREMENT_DOC_POLICY,
			"第十二条 邮件往来、客户单方请求不构成交货期变更。",
			"Sales replies within 2 working days.\nLate replies are escalated.")

		Convey("A quote copied from a block is found, with the block and character offsets\n", func() {
			c, ok := locateQuote(d, "客户单方请求不构成交货期变更")
			So(ok, ShouldBeTrue)
			So(c.BlockID, ShouldEqual, "b1")
			So(string([]rune(d.Content)[c.Start:c.End]), ShouldEqual, "客户单方请求不构成交货期变更")
		})

		Convey("Whitespace and full-width forms may differ; the offsets are the document's own\n", func() {
			c, ok := locateQuote(d, "  Sales   replies within ２ working\tdays. ")
			So(ok, ShouldBeTrue)
			So(c.BlockID, ShouldEqual, "b2")
			So(string([]rune(d.Content)[c.Start:c.End]), ShouldEqual, "Sales replies within 2 working days.")
			c, ok = locateQuote(d, "days. Late replies")
			So(ok, ShouldBeTrue)
			So(string([]rune(d.Content)[c.Start:c.End]), ShouldEqual, "days.\nLate replies")
		})

		Convey("A reworded, joined or too short quote is not found\n", func() {
			for _, quote := range []string{
				"客户单方请求构成交货期变更",        // reworded
				"交货期变更。 Sales replies", // spans two blocks
				"变更",                   // too short to mean anything
				"   ",
				"Sales replies within 3 working days.",
			} {
				_, ok := locateQuote(d, quote)
				So(ok, ShouldBeFalse)
			}
		})
	})
}

func Test_CheckCitations(t *testing.T) {
	Convey("Test checkCitations\n", t, func() {
		docs := documentsByID([]*interfaces.RequirementDocument{
			doc("d1", interfaces.REQUIREMENT_DOC_POLICY, "Requests by email do not change the agreed date."),
		})

		Convey("Found quotes are kept and located; quotes not found are dropped\n", func() {
			kept, reason := checkCitations([]interfaces.RequirementCitation{
				{DocumentID: "d1", Quote: "do not change the agreed date"},
				{DocumentID: "d1", Quote: "always change the date"},
			}, docs)
			So(reason, ShouldEqual, "")
			So(kept, ShouldHaveLength, 1)
			So(kept[0].BlockID, ShouldEqual, "b1")
			So(kept[0].End, ShouldBeGreaterThan, kept[0].Start)
		})

		Convey("Without a quote found, the reason says whether the document was unknown\n", func() {
			_, reason := checkCitations([]interfaces.RequirementCitation{{DocumentID: "d1", Quote: "not in it at all"}}, docs)
			So(reason, ShouldEqual, rejectedQuoteNotFound)
			_, reason = checkCitations([]interfaces.RequirementCitation{{DocumentID: "d9", Quote: "do not change the agreed date"}}, docs)
			So(reason, ShouldEqual, rejectedUnknownDocument)
			_, reason = checkCitations([]interfaces.RequirementCitation{
				{DocumentID: "d9", Quote: "x y z w"}, {DocumentID: "d1", Quote: "not in it at all"},
			}, docs)
			So(reason, ShouldEqual, rejectedQuoteNotFound)
		})

		Convey("No citations at all is no reason by itself: the caller decides whether a basis is needed\n", func() {
			kept, reason := checkCitations(nil, docs)
			So(kept, ShouldBeEmpty)
			So(reason, ShouldEqual, "")
		})
	})
}

func Test_NumbersInQuotes(t *testing.T) {
	Convey("Test numbersInQuotes\n", t, func() {
		quotes := []interfaces.RequirementCitation{{Quote: "应在2个工作日内答复，编号格式 SO1234567。"}}

		Convey("Every number in the text must be a number in the quotes, full-width or not\n", func() {
			So(numbersInQuotes("Reply within ２ working days", quotes), ShouldBeTrue)
			So(numbersInQuotes("编号 SO1234567 两个工作日", quotes), ShouldBeTrue)
			So(numbersInQuotes("No numbers here", quotes), ShouldBeTrue)
			So(numbersInQuotes("Reply within 3 working days", quotes), ShouldBeFalse)
			So(numbersInQuotes("Reply within ３ working days", quotes), ShouldBeFalse)
		})

		Convey("A number is matched whole, not inside a longer one\n", func() {
			So(numbersInQuotes("1234", quotes), ShouldBeFalse)
			So(numbersInQuotes("Order 12", []interfaces.RequirementCitation{{Quote: "since 2012"}}), ShouldBeFalse)
		})

		Convey("A decimal is one number: 0.5% is not 5%\n", func() {
			quoted := []interfaces.RequirementCitation{{Quote: "误差不超过0.5%，单价 12.30 元。"}}
			So(numbersInQuotes("误差不超过5%", quoted), ShouldBeFalse)
			So(numbersInQuotes("误差不超过0.5%", quoted), ShouldBeTrue)
			So(numbersInQuotes("单价 12.30 元", quoted), ShouldBeTrue)
			So(numbersInQuotes("单价 30 元", quoted), ShouldBeFalse)
			// A full stop after a number ends it.
			So(numbersInQuotes("Within 2. Then 5.", []interfaces.RequirementCitation{{Quote: "within 2 days, then 5"}}), ShouldBeTrue)
		})

		Convey("Dates split into their numbers\n", func() {
			dated := []interfaces.RequirementCitation{{Quote: "effective 2026-04-30"}}
			So(numbersInQuotes("from 2026-04-30", dated), ShouldBeTrue)
			So(numbersInQuotes("from 2026-05-30", dated), ShouldBeFalse)
		})
	})
}

func Test_SuggestedDocument(t *testing.T) {
	Convey("Test suggestedDocument\n", t, func() {
		docs := documentsByID([]*interfaces.RequirementDocument{
			{ID: "std", DocType: interfaces.REQUIREMENT_DOC_STANDARD},
			{ID: "pol", DocType: interfaces.REQUIREMENT_DOC_POLICY},
			{ID: "prd", DocType: interfaces.REQUIREMENT_DOC_PRD},
			{ID: "prd2", DocType: interfaces.REQUIREMENT_DOC_PRD},
			{ID: "oth", DocType: interfaces.REQUIREMENT_DOC_OTHER},
		})
		side := func(id string) interfaces.RequirementConflictSide {
			return interfaces.RequirementConflictSide{RequirementCitation: interfaces.RequirementCitation{DocumentID: id}}
		}

		Convey("Terms and rules follow standard over policy over PRD\n", func() {
			for _, kind := range []string{conflictTerm, conflictRule} {
				So(suggestedDocument(kind, []interfaces.RequirementConflictSide{side("prd"), side("pol")}, docs), ShouldEqual, "pol")
				So(suggestedDocument(kind, []interfaces.RequirementConflictSide{side("pol"), side("std")}, docs), ShouldEqual, "std")
				So(suggestedDocument(kind, []interfaces.RequirementConflictSide{side("oth"), side("prd")}, docs), ShouldEqual, "prd")
			}
		})

		Convey("Goals, scope and scenarios follow the PRD\n", func() {
			for _, kind := range []string{conflictGoal, conflictScope, conflictScenario} {
				So(suggestedDocument(kind, []interfaces.RequirementConflictSide{side("std"), side("prd")}, docs), ShouldEqual, "prd")
				So(suggestedDocument(kind, []interfaces.RequirementConflictSide{side("std"), side("pol")}, docs), ShouldEqual, "")
			}
		})

		Convey("Sides of equal authority, an unknown kind or document get no suggestion\n", func() {
			So(suggestedDocument(conflictRule, []interfaces.RequirementConflictSide{side("prd"), side("prd2")}, docs), ShouldEqual, "")
			So(suggestedDocument("other", []interfaces.RequirementConflictSide{side("prd"), side("std")}, docs), ShouldEqual, "")
			So(suggestedDocument(conflictRule, []interfaces.RequirementConflictSide{side("std"), side("gone")}, docs), ShouldEqual, "")
			So(suggestedDocument(conflictRule, []interfaces.RequirementConflictSide{side("std")}, docs), ShouldEqual, "")
		})

		Convey("Two quotes of one document are one side\n", func() {
			So(suggestedDocument(conflictRule, []interfaces.RequirementConflictSide{side("std"), side("std"), side("pol")}, docs), ShouldEqual, "std")
			So(suggestedDocument(conflictRule, []interfaces.RequirementConflictSide{side("prd"), side("prd")}, docs), ShouldEqual, "")
		})
	})
}
