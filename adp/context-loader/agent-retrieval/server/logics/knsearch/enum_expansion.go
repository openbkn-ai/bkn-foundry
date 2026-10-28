// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knsearch

import (
	"context"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// defaultEnumExpandLimit is the most enum items a property lists when the deployment sets none.
const defaultEnumExpandLimit = 50

func (s *localSearchImpl) enumExpandLimit() int {
	if s.config != nil && s.config.ConceptSearchConfig.EnumExpandLimit > 0 {
		return s.config.ConceptSearchConfig.EnumExpandLimit
	}
	return defaultEnumExpandLimit
}

// expandEnums fills in the enum of each returned property that names a value type
// (SPECIFICATION.md §16). Stored data holds codes, so without the enum a caller sees only a value
// type id and has to guess which codes mean what.
//
//   - In full mode an enum of at most the expansion limit lists all its items, deprecated ones
//     included: stored data still holds them, and a filter for "revoked" must take the old code too.
//   - A larger enum, or any enum in brief mode, gives its item count instead. enum_query then lists
//     the items whose display name or code contains one of its words, up to the expansion limit.
//   - A value type that is not an enum adds nothing, so value_type is dropped with it.
//
// The value types are one read, made only when some property names one, and bounded by the schema
// read timeout. When it fails or times out the properties keep their value_type and go without the
// enum.
func (s *localSearchImpl) expandEnums(ctx context.Context, knID string, objectTypes []*interfaces.KnSearchObjectType,
	relationTypes []*interfaces.KnSearchRelationType, brief bool, enumQuery string) {
	var props []*interfaces.KnSearchDataProperty
	for _, obj := range objectTypes {
		props = append(props, obj.DataProperties...)
	}
	for _, rel := range relationTypes {
		props = append(props, rel.DataProperties...)
	}
	named := props[:0]
	for _, p := range props {
		if p != nil && p.ValueType != "" {
			named = append(named, p)
		}
	}
	if len(named) == 0 {
		return
	}

	reader, ok := s.bknBackend.(interfaces.ValueTypeReader)
	if !ok {
		return
	}
	readCtx, cancel := context.WithTimeout(ctx, s.schemaReadTimeout())
	defer cancel()
	valueTypes, err := reader.ListValueTypes(readCtx, knID)
	if err != nil {
		s.logger.WithContext(ctx).Warnf("[ConceptRetrieval] value types unreadable, enums not expanded: %v", err)
		return
	}
	byID := make(map[string]*interfaces.ValueType, len(valueTypes))
	for _, vt := range valueTypes {
		if vt != nil {
			byID[vt.ID] = vt
		}
	}

	limit := s.enumExpandLimit()
	words := strings.Fields(strings.ToLower(enumQuery))
	for _, p := range named {
		vt := byID[p.ValueType]
		if vt == nil || len(vt.Enum) == 0 {
			p.ValueType = ""
			continue
		}
		if !brief && len(vt.Enum) <= limit {
			p.Enum, _ = enumItems(vt.Enum, nil, limit)
			continue
		}
		p.EnumCount = len(vt.Enum)
		if len(words) > 0 {
			var matched int
			p.Enum, matched = enumItems(vt.Enum, words, limit)
			if matched > len(p.Enum) {
				p.EnumMatchCount = matched
			}
		}
	}
}

// enumItems lists the enum items that match one of words, all of them when words is empty, up to
// limit items. It also returns how many items matched, so a cut list can say so.
func enumItems(items []*interfaces.ValueTypeEnum, words []string, limit int) ([]*interfaces.KnSearchEnumItem, int) {
	var out []*interfaces.KnSearchEnumItem
	matched := 0
	for _, item := range items {
		if item == nil || (len(words) > 0 && !enumItemMatches(item, words)) {
			continue
		}
		matched++
		if len(out) < limit {
			out = append(out, &interfaces.KnSearchEnumItem{
				Value: item.Value, Display: item.Display, Deprecated: item.Deprecated,
			})
		}
	}
	return out, matched
}

func enumItemMatches(item *interfaces.ValueTypeEnum, words []string) bool {
	value, display := strings.ToLower(item.Value), strings.ToLower(item.Display)
	for _, w := range words {
		if strings.Contains(value, w) || strings.Contains(display, w) {
			return true
		}
	}
	return false
}
