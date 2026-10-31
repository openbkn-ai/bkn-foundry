// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// Revisions Ask AI proposes are checked against the item as the editor has it, by the rules the
// item's save applies; none is applied here.

// workingObjectType is the object type as the editor's unsaved form has it: its name, comment,
// identity and title where given, and its kept properties with their display names and comments.
// A property the form adds is not yet one of the draft, so revisions cannot name it.
func workingObjectType(objectType *interfaces.DraftObjectType, working *interfaces.DraftObjectTypeSave) *interfaces.DraftObjectType {
	base := *objectType
	base.Properties = nil
	for _, p := range objectType.Properties {
		property := *p
		base.Properties = append(base.Properties, &property)
	}
	if working == nil {
		return &base
	}
	if name := strings.TrimSpace(working.Name); name != "" {
		base.Name = name
	}
	if comment := strings.TrimSpace(working.Comment); comment != "" {
		base.Description = comment
	}
	if working.PrimaryKeys != nil {
		base.PrimaryKeys = append([]string{}, working.PrimaryKeys...)
		base.NeedsIdentity = len(working.PrimaryKeys) == 0
	}
	if key := strings.TrimSpace(working.DisplayKey); key != "" {
		base.DisplayKey = key
	}
	if working.DataProperties != nil {
		kept := []*interfaces.DraftProperty{}
		for _, saved := range working.DataProperties {
			p := findProperty(&base, saved.Name)
			if p == nil {
				continue
			}
			if displayName := strings.TrimSpace(saved.DisplayName); displayName != "" {
				p.DisplayName = displayName
			}
			p.Description = strings.TrimSpace(saved.Comment)
			kept = append(kept, p)
		}
		base.Properties = kept
	}
	return &base
}

// workingRelationType is the relation type as the editor's unsaved form has it: its name and
// comment where given, and its ends and mapping when the save would take them.
func (s *draftService) workingRelationType(ctx context.Context, items *interfaces.DraftItems,
	relationType *interfaces.DraftRelationType, working *interfaces.DraftRelationTypeSave,
	caller interfaces.AccountInfo) *interfaces.DraftRelationType {
	base := *relationType
	if working == nil {
		return &base
	}
	if name := strings.TrimSpace(working.Name); name != "" {
		base.Name = name
	}
	if comment := strings.TrimSpace(working.Comment); comment != "" {
		base.Description = comment
	}
	if working.Type != "" {
		defined := base
		if s.defineRelation(ctx, items, &defined, *working, caller) == nil {
			base = defined
		}
	}
	return &base
}

// checkRevisions keeps the revisions the check takes, one for each field, or each property, and
// drops the others with why.
func checkRevisions(outs []askRevisionOutput, editable []string,
	check func(askRevisionOutput) (*interfaces.DraftAskRevision, string)) *interfaces.DraftAskAnswer {
	answer := &interfaces.DraftAskAnswer{Revisions: []interfaces.DraftAskRevision{}, Dropped: []interfaces.DraftAskDropped{}}
	seen := map[string]bool{}
	for _, out := range outs[:min(len(outs), askMaxRevisions)] {
		key := out.Field
		if out.Field == interfaces.DRAFT_ASK_REVISION_DATA_PROPERTY && out.Property != nil {
			key += "/" + out.Property.Name
		}
		var revision *interfaces.DraftAskRevision
		reason := askDroppedNotEditable
		switch {
		case !slices.Contains(editable, out.Field):
		case seen[key]:
			reason = askDroppedRepeated
		default:
			revision, reason = check(out)
		}
		if revision == nil {
			answer.Dropped = append(answer.Dropped, interfaces.DraftAskDropped{Field: out.Field, Reason: reason})
			continue
		}
		seen[key] = true
		revision.Field = out.Field
		revision.Reason = truncateRunes(strings.TrimSpace(out.Reason), askReasonMaxLength)
		answer.Revisions = append(answer.Revisions, *revision)
	}
	return answer
}

// nameOrComment checks a name or a comment as an item's edit does; current is the item's own.
func nameOrComment(ctx context.Context, field string, value string, current string) (*interfaces.DraftAskRevision, string) {
	edit, reason := interfaces.DraftItemEdit{Description: &value}, askDroppedInvalidDescription
	if field == interfaces.DRAFT_ASK_REVISION_NAME {
		edit, reason = interfaces.DraftItemEdit{Name: &value}, askDroppedInvalidName
	}
	if checkEdit(ctx, &edit) != nil {
		return nil, reason
	}
	if edit.Name != nil {
		value = *edit.Name
	} else {
		value = *edit.Description
	}
	if value == "" || value == current {
		return nil, askDroppedUnchanged
	}
	return &interfaces.DraftAskRevision{Value: value}, ""
}

// current is an item's own name or description, as the field names it.
func current(field string, name string, description string) string {
	if field == interfaces.DRAFT_ASK_REVISION_NAME {
		return name
	}
	return description
}

// objectTypeRevision checks one revision of an object type as its save does.
func objectTypeRevision(ctx context.Context, base *interfaces.DraftObjectType,
	out askRevisionOutput) (*interfaces.DraftAskRevision, string) {
	switch out.Field {
	case interfaces.DRAFT_ASK_REVISION_NAME, interfaces.DRAFT_ASK_REVISION_COMMENT:
		return nameOrComment(ctx, out.Field, out.Value, current(out.Field, base.Name, base.Description))
	case interfaces.DRAFT_ASK_REVISION_PRIMARY_KEYS:
		keys := out.Properties
		if _, err := identityProperties(ctx, base, keys); err != nil {
			return nil, askDroppedInvalidIdentity
		}
		if !base.NeedsIdentity && slices.Equal(keys, base.PrimaryKeys) {
			return nil, askDroppedUnchanged
		}
		return &interfaces.DraftAskRevision{Value: append([]string{}, keys...)}, ""
	case interfaces.DRAFT_ASK_REVISION_DISPLAY_KEY:
		key := strings.TrimSpace(out.Value)
		if findProperty(base, key) == nil {
			return nil, askDroppedUnknownProperty
		}
		if key == base.DisplayKey {
			return nil, askDroppedUnchanged
		}
		return &interfaces.DraftAskRevision{Value: key}, ""
	case interfaces.DRAFT_ASK_REVISION_DATA_PROPERTY:
		if out.Property == nil {
			return nil, askDroppedUnknownProperty
		}
		p := findProperty(base, out.Property.Name)
		if p == nil {
			return nil, askDroppedUnknownProperty
		}
		// What is left empty keeps the property's own, so saving the revision clears nothing.
		displayName, comment := strings.TrimSpace(out.Property.DisplayName), strings.TrimSpace(out.Property.Comment)
		if displayName == "" {
			displayName = p.DisplayName
		}
		if comment == "" {
			comment = p.Description
		}
		if utf8.RuneCountInString(displayName) > interfaces.DRAFT_NAME_MAX_LENGTH ||
			utf8.RuneCountInString(comment) > interfaces.DRAFT_DESCRIPTION_MAX_LENGTH {
			return nil, askDroppedTooLong
		}
		if displayName == p.DisplayName && comment == p.Description {
			return nil, askDroppedUnchanged
		}
		return &interfaces.DraftAskRevision{Value: interfaces.DraftAskPropertyRevision{
			Name: p.Name, DisplayName: displayName, Comment: comment}}, ""
	}
	return nil, askDroppedNotEditable
}

// relationTypeRevision checks one revision of a relation type as its save does. Ends are a direct
// mapping, defined on a copy, so nothing is read and nothing counted.
func (s *draftService) relationTypeRevision(ctx context.Context, items *interfaces.DraftItems,
	base *interfaces.DraftRelationType, out askRevisionOutput, caller interfaces.AccountInfo) (*interfaces.DraftAskRevision, string) {
	switch out.Field {
	case interfaces.DRAFT_ASK_REVISION_NAME, interfaces.DRAFT_ASK_REVISION_COMMENT:
		return nameOrComment(ctx, out.Field, out.Value, current(out.Field, base.Name, base.Description))
	case interfaces.DRAFT_ASK_REVISION_ENDS:
		ends := interfaces.DraftAskEndsRevision{SourceObjectTypeID: out.Source, TargetObjectTypeID: out.Target,
			Type: interfaces.DRAFT_RELATION_DIRECT, MappingRules: []interfaces.DraftRelationMapping{}}
		if ends.SourceObjectTypeID == "" {
			ends.SourceObjectTypeID = base.Source
		}
		if ends.TargetObjectTypeID == "" {
			ends.TargetObjectTypeID = base.Target
		}
		for _, m := range out.Mapping {
			ends.MappingRules = append(ends.MappingRules, interfaces.DraftRelationMapping{
				SourceProperty: interfaces.DraftMappedField{Name: m.SourceProperty},
				TargetProperty: interfaces.DraftMappedField{Name: m.TargetProperty}})
		}
		rules, err := json.Marshal(ends.MappingRules)
		if err != nil {
			return nil, askDroppedInvalid
		}
		defined := *base
		if err := s.defineRelation(ctx, items, &defined, interfaces.DraftRelationTypeSave{
			SourceObjectTypeID: ends.SourceObjectTypeID, TargetObjectTypeID: ends.TargetObjectTypeID,
			Type: ends.Type, MappingRules: rules}, caller); err != nil {
			return nil, refusedBecause(err)
		}
		if sameJoin(relationSnapshot(base), relationSnapshot(&defined)) {
			return nil, askDroppedUnchanged
		}
		return &interfaces.DraftAskRevision{Value: ends}, ""
	}
	return nil, askDroppedNotEditable
}

// refusedBecause is the reason a refusal's details give.
func refusedBecause(err error) string {
	var httpErr *httperrors.HTTPError
	if errors.As(err, &httpErr) {
		if details, ok := httpErr.ErrorDetails.(map[string]any); ok {
			if reason, ok := details["reason"].(string); ok {
				return reason
			}
		}
	}
	return askDroppedInvalid
}

// suggestionOf is the first revision the object type's own edits take, as the single suggestion:
// its name or description by EditDraftItem, its identity by ChooseIdentity.
func suggestionOf(revisions []interfaces.DraftAskRevision) *interfaces.DraftAskSuggestion {
	for _, r := range revisions {
		switch value := r.Value.(type) {
		case string:
			switch {
			case r.Field == interfaces.DRAFT_ASK_REVISION_NAME:
				return &interfaces.DraftAskSuggestion{Field: interfaces.DRAFT_ASK_FIELD_NAME, Value: value, Reason: r.Reason}
			case r.Field == interfaces.DRAFT_ASK_REVISION_COMMENT && utf8.RuneCountInString(value) <= askValueMaxLength:
				return &interfaces.DraftAskSuggestion{Field: interfaces.DRAFT_ASK_FIELD_DESCRIPTION, Value: value, Reason: r.Reason}
			}
		case []string:
			return &interfaces.DraftAskSuggestion{Field: interfaces.DRAFT_ASK_FIELD_IDENTITY, Properties: value, Reason: r.Reason}
		}
	}
	return nil
}
