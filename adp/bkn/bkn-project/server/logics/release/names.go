// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package release

import (
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// minSimilarRunes is the shortest name, without separators, that one character can make similar to
// another: below it, names one character apart are as often different things.
const minSimilarRunes = 4

// nameSeparators are left out when telling whether two names are alike.
var nameSeparators = strings.NewReplacer(" ", "", "_", "", "-", "", "·", "", "・", "")

// compareNames compares each added item's name with the names of the other items of its kind:
// those the same but for case, character width and surrounding space, and those similar, the same
// but for separators or one character apart. Two added items alike are listed once. Pairs are in the
// order of the added items' ids, then the others'.
func compareNames(added []interfaces.ReleaseItem, all []interfaces.ReleaseItem) (same []interfaces.SimilarNames,
	similar []interfaces.SimilarNames) {
	isAdded := map[interfaces.ReleaseItem]bool{}
	for _, a := range added {
		isAdded[a] = true
	}
	same, similar = []interfaces.SimilarNames{}, []interfaces.SimilarNames{}
	for _, a := range added {
		for _, o := range all {
			if o.Kind != a.Kind || o.ID == a.ID || (isAdded[o] && o.ID < a.ID) {
				continue
			}
			pair := interfaces.SimilarNames{Added: a, Other: o}
			switch {
			case nameKey(a.Name) == nameKey(o.Name):
				same = append(same, pair)
			case alike(a.Name, o.Name):
				similar = append(similar, pair)
			}
		}
	}
	for _, pairs := range [][]interfaces.SimilarNames{same, similar} {
		sort.Slice(pairs, func(i, j int) bool {
			if pairs[i].Added.ID != pairs[j].Added.ID {
				return pairs[i].Added.ID < pairs[j].Added.ID
			}
			return pairs[i].Other.ID < pairs[j].Other.ID
		})
	}
	return same, similar
}

// nameKey is a name without case, surrounding space or full-width forms.
func nameKey(name string) string {
	return strings.ToLower(strings.TrimSpace(strings.Map(halfWidth, name)))
}

// alike tells two names the same but for separators, or one character apart when long enough.
func alike(a string, b string) bool {
	ca, cb := nameSeparators.Replace(nameKey(a)), nameSeparators.Replace(nameKey(b))
	if ca == cb {
		return true
	}
	if min(utf8.RuneCountInString(ca), utf8.RuneCountInString(cb)) < minSimilarRunes {
		return false
	}
	return editDistance([]rune(ca), []rune(cb)) <= 1
}

// halfWidth maps a full-width ASCII character, or the ideographic space, to its half-width form.
func halfWidth(r rune) rune {
	switch {
	case r == 0x3000:
		return ' '
	case r >= 0xff01 && r <= 0xff5e:
		return r - 0xfee0
	}
	return r
}

// editDistance counts the characters to insert, delete or replace to turn a into b.
func editDistance(a []rune, b []rune) int {
	previous := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(a); i++ {
		current := make([]int, len(b)+1)
		current[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
		}
		previous = current
	}
	return previous[len(b)]
}
