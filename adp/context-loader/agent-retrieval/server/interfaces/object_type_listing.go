// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "strings"

// ObjectTypeIndexCommentRunes is how much of an object type's comment a listed entry
// keeps, as for a relation type: enough to tell what the object type is. The rest comes
// with the definition, by naming the id back.
const ObjectTypeIndexCommentRunes = RelationTypeIndexCommentRunes

// MaxObjectTypePageChars bounds one listed page, measured on what the caller receives
// (see MaxRelationTypePageChars). Object type comments are often the longest prose in a
// network, so a page of 200 entries can pass what a harness accepts in one result even
// with the comments shortened. A variable so tests can reach it with small pages.
var MaxObjectTypePageChars = 30000

// ShortenObjectTypeComments cuts each listed object type's comment to its first
// ObjectTypeIndexCommentRunes runes, marked with an ellipsis when it was cut.
func ShortenObjectTypeComments(objectTypes []*ObjectType) {
	for _, o := range objectTypes {
		if o != nil {
			o.Comment = commentPreview(o.Comment)
		}
	}
}

// FitObjectTypePage cuts a listed page to the longest leading part whose size stays
// within limit, and reports how many entries it kept. A page that fits is left as it
// is. A cut page continues at the first entry it left out, and its notice says why,
// after any notice it already had; it always keeps one entry, so a walk moves on even
// past an entry that is too big alone.
//
// A walk advances by what the listing scanned (ObjectTypePage.Scanned), not by what the
// page carries, so a cut cannot reuse the offset NextObjectTypeOffset computed for the
// whole page: that one points past the entries the cut left out. The cut continues at
// the first entry it left out, which is offset+kept because a listed page carries every
// entry its window scanned (both readers set Scanned to the entries they return). A
// reader that dropped entries after paging would need to say where the survivors sat;
// until one does, offset+kept is the earliest position the first left-out entry can
// hold, so a walk never skips one.
func FitObjectTypePage(resp *ObjectTypesResp, offset, limit int, notice func(kept int) string,
	size func(*ObjectTypesResp) (int, error)) (int, error) {
	all := resp.ObjectTypes
	n, err := size(resp)
	if err != nil || n <= limit || len(all) <= 1 {
		return len(all), err
	}
	earlier := resp.Notice
	keep := func(kept int) {
		next := offset + kept
		resp.ObjectTypes, resp.NextOffset = all[:kept], &next
		resp.Notice = strings.TrimSpace(earlier + " " + notice(kept))
	}
	// The largest kept count that fits; size grows with it.
	lo, hi := 1, len(all)-1
	best := 1
	for lo <= hi {
		mid := (lo + hi) / 2
		keep(mid)
		n, err := size(resp)
		if err != nil {
			return 0, err
		}
		if n <= limit {
			best, lo = mid, mid+1
		} else {
			hi = mid - 1
		}
	}
	keep(best)
	return best, nil
}
