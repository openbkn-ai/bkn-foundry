// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// The agent quotes; these rules check that every quote is in the documents. They judge only what
// the text says, never whether the agent understood it: that is for a person to review.

// Why a drafted item was dropped, stored as codes for the client to word.
const (
	rejectedQuoteNotFound    = "quote_not_found"
	rejectedUnknownDocument  = "unknown_document"
	rejectedNumberNotInQuote = "number_not_in_quote"
)

// minQuoteRunes is the shortest quote taken as a basis, not counting spaces: a word or two is
// found almost anywhere and shows nothing.
const minQuoteRunes = 4

// normalized is text as quotes are compared: full-width forms as their ASCII ones and every run
// of whitespace as one space, trimmed. at maps each of its characters to the character of the
// original text it came from.
type normalized struct {
	text string
	at   []int
}

func normalize(runes []rune) normalized {
	var b strings.Builder
	at := make([]int, 0, len(runes))
	space := false
	for i, r := range runes {
		switch {
		case r == '\u3000' || unicode.IsSpace(r):
			space = len(at) > 0
			continue
		case r >= '\uff01' && r <= '\uff5e':
			r -= 0xfee0
		}
		if space {
			b.WriteByte(' ')
			at = append(at, i-1)
			space = false
		}
		b.WriteRune(r)
		at = append(at, i)
	}
	return normalized{text: b.String(), at: at}
}

// locateQuote finds quote within one block of the document, as copied from it, and returns the
// citation with the block and the document's own character offsets.
func locateQuote(doc *interfaces.RequirementDocument, quote string) (interfaces.RequirementCitation, bool) {
	q := normalize([]rune(quote))
	if utf8.RuneCountInString(strings.ReplaceAll(q.text, " ", "")) < minQuoteRunes {
		return interfaces.RequirementCitation{}, false
	}
	content := []rune(doc.Content)
	for _, block := range doc.Blocks {
		if block.Start < 0 || block.End > len(content) || block.Start >= block.End {
			continue
		}
		text := normalize(content[block.Start:block.End])
		i := strings.Index(text.text, q.text)
		if i < 0 {
			continue
		}
		first := utf8.RuneCountInString(text.text[:i])
		last := first + utf8.RuneCountInString(q.text) - 1
		return interfaces.RequirementCitation{
			DocumentID: doc.ID, Quote: quote, BlockID: block.ID,
			Start: block.Start + text.at[first], End: block.Start + text.at[last] + 1,
		}, true
	}
	return interfaces.RequirementCitation{}, false
}

// exampleCitation finds a value standing whole in a block of the documents, in id order, and cites
// the block: a value an ASCII letter, digit, hyphen or underscore continues is part of a longer one.
func exampleCitation(value string, docs map[string]*interfaces.RequirementDocument) (interfaces.RequirementCitation, bool) {
	v := normalize([]rune(value)).text
	if v == "" {
		return interfaces.RequirementCitation{}, false
	}
	ids := make([]string, 0, len(docs))
	for id := range docs {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		d := docs[id]
		content := []rune(d.Content)
		for _, b := range d.Blocks {
			if b.Start < 0 || b.End > len(content) || b.Start >= b.End {
				continue
			}
			if standsWhole(normalize(content[b.Start:b.End]).text, v) {
				return interfaces.RequirementCitation{DocumentID: d.ID, Quote: string(content[b.Start:b.End]),
					BlockID: b.ID, Start: b.Start, End: b.End}, true
			}
		}
	}
	return interfaces.RequirementCitation{}, false
}

// standsWhole says value stands in text not as part of a longer code or word written in ASCII.
func standsWhole(text string, value string) bool {
	return len(wholeAt(text, value)) > 0
}

// wholeAt are the byte offsets where value stands in text whole: not continued, on either side,
// by an ASCII letter, digit, hyphen or underscore when its own edge is one.
func wholeAt(text string, value string) []int {
	joins := func(r rune) bool {
		return r < utf8.RuneSelf && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_')
	}
	if value == "" {
		return nil
	}
	first, _ := utf8.DecodeRuneInString(value)
	last, _ := utf8.DecodeLastRuneInString(value)
	var at []int
	for from := 0; ; {
		i := strings.Index(text[from:], value)
		if i < 0 {
			return at
		}
		i += from
		before, _ := utf8.DecodeLastRuneInString(text[:i])
		after, _ := utf8.DecodeRuneInString(text[i+len(value):])
		if !(i > 0 && joins(first) && joins(before)) && !(i+len(value) < len(text) && joins(last) && joins(after)) {
			at = append(at, i)
		}
		from = i + 1
	}
}

// citeExample makes a question cite the block an example stands in: unless a quote already says
// it, a citation of that block is widened to the block, or the block is cited.
func citeExample(citations []interfaces.RequirementCitation, example string,
	block interfaces.RequirementCitation) []interfaces.RequirementCitation {
	v := normalize([]rune(example)).text
	for i, c := range citations {
		if standsWhole(normalize([]rune(c.Quote)).text, v) {
			return citations
		}
		if c.DocumentID == block.DocumentID && c.BlockID == block.BlockID {
			citations[i] = block
			return citations
		}
	}
	return append(citations, block)
}

func documentsByID(docs []*interfaces.RequirementDocument) map[string]*interfaces.RequirementDocument {
	byID := make(map[string]*interfaces.RequirementDocument, len(docs))
	for _, d := range docs {
		byID[d.ID] = d
	}
	return byID
}

// checkCitations keeps the citations whose quotes are found, located. When some were given and
// none is found, reason says why: unknown_document when every one named a document the project
// does not have, quote_not_found otherwise.
func checkCitations(citations []interfaces.RequirementCitation,
	docs map[string]*interfaces.RequirementDocument) ([]interfaces.RequirementCitation, string) {
	kept := []interfaces.RequirementCitation{}
	known := false
	for _, c := range citations {
		d, ok := docs[c.DocumentID]
		if !ok {
			continue
		}
		known = true
		if located, ok := locateQuote(d, c.Quote); ok {
			kept = append(kept, located)
		}
	}
	switch {
	case len(citations) == 0 || len(kept) > 0:
		return kept, ""
	case !known:
		return kept, rejectedUnknownDocument
	default:
		return kept, rejectedQuoteNotFound
	}
}

// numbers lists the numbers in text, full-width digits and points as ASCII: runs of digits, with
// a single decimal point between two digits kept inside the number, so 0.5 is not 5. A date or a code gives
// one number per group of digits.
func numbers(text string) []string {
	runes := []rune(text)
	digit := func(i int) (rune, bool) {
		if i < 0 || i >= len(runes) {
			return 0, false
		}
		r := runes[i]
		if r >= '\uff10' && r <= '\uff19' {
			r -= 0xfee0
		}
		return r, r >= '0' && r <= '9'
	}
	var found []string
	var run strings.Builder
	for i := range runes {
		if r, ok := digit(i); ok {
			run.WriteRune(r)
			continue
		}
		if (runes[i] == '.' || runes[i] == '\uff0e') && run.Len() > 0 {
			if _, next := digit(i + 1); next {
				run.WriteRune('.')
				continue
			}
		}
		if run.Len() > 0 {
			found = append(found, run.String())
			run.Reset()
		}
	}
	if run.Len() > 0 {
		found = append(found, run.String())
	}
	return found
}

// numbersInQuotes tells whether every number in text is a number of one of the quotes, whole: a
// rule or question must not state a figure, date or code its sources do not.
func numbersInQuotes(text string, citations []interfaces.RequirementCitation) bool {
	quoted := map[string]bool{}
	for _, c := range citations {
		for _, n := range numbers(c.Quote) {
			quoted[n] = true
		}
	}
	for _, n := range numbers(text) {
		if !quoted[n] {
			return false
		}
	}
	return true
}
