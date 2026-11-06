// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// Aliases are an item's other names, which agents search and understand it by. People edit them as
// BKN accepts them (bkn-specification declarations.go): a save BKN would refuse is refused, saying
// which alias and why, so nothing a person entered is left out later without a word.

// Why an alias is refused, as the error details name it.
const (
	aliasTooMany    = "too_many"
	aliasTooLong    = "too_long"
	aliasComma      = "comma"
	aliasRepeated   = "repeated"
	aliasOwnName    = "own_name"
	aliasOtherName  = "other_name"
	aliasOfAnother  = "alias_of_other"
	aliasOnDocument = "document_item"
)

// aliasError refuses an alias of the item named item: other names the item whose name it is, or,
// for aliasOfAnother, the item that has the name as its alias.
func aliasError(ctx context.Context, item string, alias string, reason string, other string) error {
	details := map[string]any{"item": item, "alias": alias, "reason": reason}
	if other != "" {
		details["other"] = other
	}
	return httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidAliases).
		WithErrorDetails(details)
}

// cleanAliases trims each alias and leaves out empty ones; nil when none is left.
func cleanAliases(aliases []string) []string {
	var cleaned []string
	for _, alias := range aliases {
		if alias = strings.TrimSpace(alias); alias != "" {
			cleaned = append(cleaned, alias)
		}
	}
	return cleaned
}

// checkOwnAliases refuses aliases BKN refuses of one item on their own: too many, too long, holding
// a comma, repeated, or one of the item's own names.
func checkOwnAliases(ctx context.Context, item string, aliases []string, own ...string) error {
	if len(aliases) > interfaces.DRAFT_ALIASES_MAX {
		return aliasError(ctx, item, "", aliasTooMany, "")
	}
	seen := map[string]bool{}
	for _, alias := range aliases {
		switch {
		case utf8.RuneCountInString(alias) > interfaces.DRAFT_ALIAS_MAX_LENGTH:
			return aliasError(ctx, item, alias, aliasTooLong, "")
		case strings.ContainsAny(alias, ",，"):
			return aliasError(ctx, item, alias, aliasComma, "")
		case seen[alias]:
			return aliasError(ctx, item, alias, aliasRepeated, "")
		case slices.Contains(own, alias):
			return aliasError(ctx, item, alias, aliasOwnName, "")
		}
		seen[alias] = true
	}
	return nil
}

// withoutName leaves out of aliases those an item renamed to has become: they name it already.
func withoutName(aliases []string, names ...string) []string {
	kept := slices.DeleteFunc(slices.Clone(aliases), func(alias string) bool { return slices.Contains(names, alias) })
	if len(kept) == 0 {
		return nil
	}
	return kept
}

// checkObjectTypeAliases refuses an object type's aliases BKN refuses: on their own, one that is
// another object type's name, or its name being another object type's alias.
func checkObjectTypeAliases(ctx context.Context, items *interfaces.DraftItems, ot *interfaces.DraftObjectType) error {
	if err := checkOwnAliases(ctx, ot.Name, ot.Aliases, ot.Name); err != nil {
		return err
	}
	for _, other := range items.ObjectTypes {
		if other.ID == ot.ID {
			continue
		}
		if slices.Contains(ot.Aliases, other.Name) {
			return aliasError(ctx, ot.Name, other.Name, aliasOtherName, other.Name)
		}
		if slices.Contains(other.Aliases, ot.Name) {
			return aliasError(ctx, ot.Name, ot.Name, aliasOfAnother, other.Name)
		}
	}
	return nil
}

// propertyAliasConflicts are the aliases of properties that are the name or display name of
// another property, as alias/owner keys: BKN refuses each.
func propertyAliasConflicts(properties []*interfaces.DraftProperty) map[string]string {
	conflicts := map[string]string{}
	for _, p := range properties {
		for _, alias := range p.Aliases {
			for _, other := range properties {
				if other != p && (other.Name == alias || other.DisplayName == alias) {
					conflicts[p.Name+"\x00"+alias] = other.DisplayName
				}
			}
		}
	}
	return conflicts
}

// checkPropertyAliases refuses the aliases of the saved properties BKN refuses: on their own, and
// one that has become another property's name or display name. A conflict the properties had
// before the save is not the save's: the confirmation reports it.
func checkPropertyAliases(ctx context.Context, before []*interfaces.DraftProperty, after []*interfaces.DraftProperty) error {
	for _, p := range after {
		if err := checkOwnAliases(ctx, p.DisplayName, p.Aliases, p.Name, p.DisplayName); err != nil {
			return err
		}
	}
	had, has := propertyAliasConflicts(before), propertyAliasConflicts(after)
	for _, p := range after {
		for _, alias := range p.Aliases {
			key := p.Name + "\x00" + alias
			if other, conflict := has[key]; conflict && had[key] == "" {
				return aliasError(ctx, p.DisplayName, alias, aliasOtherName, other)
			}
		}
	}
	return nil
}

// leftOutAliases are the aliases of an item its definition left out.
func leftOutAliases(item interfaces.ReleaseItem, property string, drafted []string, compiled []string) []interfaces.DraftLeftOutAlias {
	var left []interfaces.DraftLeftOutAlias
	for _, alias := range drafted {
		if !slices.Contains(compiled, alias) {
			left = append(left, interfaces.DraftLeftOutAlias{Item: item, Property: property, Alias: alias})
		}
	}
	return left
}
