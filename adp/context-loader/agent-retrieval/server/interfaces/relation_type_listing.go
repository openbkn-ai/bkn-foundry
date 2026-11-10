// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// RelationTypeIndexCommentRunes is how much of a relation type's comment a listed
	// entry keeps: a sentence or two, enough to tell what the relation means. The rest
	// comes with the definition, by naming the id back.
	RelationTypeIndexCommentRunes = 120
)

// MaxRelationTypePageChars bounds one listed page, measured on what the caller
// receives: a REST body in the format they asked for, and over MCP the larger of the
// text and the structuredContent JSON beside it. An index entry without its comment is still 150-180 characters, so a
// full page of 200 can pass it even with the comments shortened. A variable so tests
// can reach it with small pages.
var MaxRelationTypePageChars = 30000

// RelationTypePage is one page of a knowledge network's relation types, with the
// total the caller may see. bkn-backend applies the caller's authorization before
// paging, so the total and the offsets are over what they can read.
type RelationTypePage struct {
	Entries    []*RelationType
	TotalCount int64
	// Scanned is how many relation types the listing covered for this window, which is
	// what a walk advances by (see ObjectTypePage.Scanned).
	Scanned int
}

// RelationTypeReader lists a network's relation types and reads them on any branch.
//
// It sits beside BknBackendAccess rather than inside it, as BranchBknBackendAccess
// does, so the test doubles of that interface need not grow two methods only
// get_relation_types uses.
type RelationTypeReader interface {
	// ListRelationTypesBranch reads one page of the relation types in name order.
	// objectTypeIDs, when given, keeps the relation types that start or end at one
	// of them.
	ListRelationTypesBranch(ctx context.Context, knID, branch string, objectTypeIDs []string,
		offset, limit int) (*RelationTypePage, error)
	// GetRelationTypeDetailBranch is GetRelationTypeDetail on a named branch.
	GetRelationTypeDetailBranch(ctx context.Context, knID, branch string, rtIDs []string,
		includeDetail bool) ([]*RelationType, error)
}

// ReduceToRelationTypeIndex keeps of each listed relation type what a caller chooses
// it by: id, name, the start of its comment, tags, its two ends and its kind, plus
// the lifecycle and fallback marking every answer carries. The whole comment, the
// mapping rules and the endpoint details come from naming the ids back to
// get_relation_types.
//
// It shares the object type index's page bounds: an index entry of either is a few
// hundred bytes.
func ReduceToRelationTypeIndex(relationTypes []*RelationType) {
	for _, r := range relationTypes {
		if r == nil {
			continue
		}
		r.ModuleType = ""
		r.Score = 0
		r.MappingRules = nil
		r.SourceObjectType = nil
		r.TargetObjectType = nil
		r.Comment = commentPreview(r.Comment)
	}
}

// commentPreview is the first RelationTypeIndexCommentRunes runes of comment, marked
// with an ellipsis when it was cut.
func commentPreview(comment string) string {
	comment = strings.TrimSpace(comment)
	if utf8.RuneCountInString(comment) <= RelationTypeIndexCommentRunes {
		return comment
	}
	cut := []rune(comment)[:RelationTypeIndexCommentRunes]
	return strings.TrimRightFunc(string(cut), unicode.IsSpace) + "…"
}

// FitRelationTypePage cuts a listed page to the longest leading part whose size
// stays within limit, and reports how many entries it kept. A page that fits is left
// as it is. A cut page continues at offset plus what it kept, and its notice says
// why, after any notice it already had; it always keeps one entry, so a walk moves
// on even past an entry that is too big alone.
//
// bkn-backend authorizes before it pages, so every listed entry counts toward the
// offset and the kept count is what the walk advances by.
func FitRelationTypePage(resp *RelationTypesResp, offset, limit int, notice func(kept int) string,
	size func(*RelationTypesResp) (int, error)) (int, error) {
	all := resp.RelationTypes
	n, err := size(resp)
	if err != nil || n <= limit || len(all) <= 1 {
		return len(all), err
	}
	earlier := resp.Notice
	keep := func(kept int) {
		next := offset + kept
		resp.RelationTypes, resp.NextOffset = all[:kept], &next
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
