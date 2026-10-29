// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// nameLanguage is the naming language a name reads in: Chinese when it holds a Han character,
// English otherwise.
func nameLanguage(name string) string {
	for _, r := range name {
		if unicode.Is(unicode.Han, r) {
			return interfaces.NAMING_LANGUAGE_ZH_CN
		}
	}
	return interfaces.NAMING_LANGUAGE_EN_US
}

// pickName names an item from its candidate names, best first, and the physical name it is
// drafted from: the first candidate in the project's naming language, else the first candidate, as
// before naming languages. The physical name names it only when there is no candidate: a name
// people read in the other language beats an abbreviation such as kh or cust_nm, and the physical
// name is never an alias. The candidates in the other language are its aliases. Empty, too long and
// repeated candidates are skipped, and aliases BKN refuses, holding a comma, are left out.
func pickName(language string, physical string, candidates ...string) (string, []string) {
	names := []string{}
	for _, c := range candidates {
		c = strings.TrimSpace(c)
		if c != "" && c != physical && utf8.RuneCountInString(c) <= maxDisplayNameRunes && !slices.Contains(names, c) {
			names = append(names, c)
		}
	}
	if len(names) == 0 {
		return physical, nil
	}
	name := names[0]
	for _, c := range names {
		if nameLanguage(c) == language {
			name = c
			break
		}
	}
	var aliases []string
	for _, c := range names {
		if c != name && nameLanguage(c) != nameLanguage(name) && !strings.ContainsAny(c, ",\uff0c") {
			aliases = append(aliases, c)
		}
	}
	return name, aliases
}

// firstClause is the first clause of a comment, empty when it has none short enough to name an item.
func firstClause(comment string) string {
	return displayName(comment, "")
}

// withNamingLanguage gives the drafter the project's naming language.
func (d *drafter) withNamingLanguage(language string) *drafter {
	d.namingLanguage = language
	return d
}

// language is the naming language drafting names in: the project's, Chinese when it has none.
func (d *drafter) language() string {
	if d.namingLanguage == "" {
		return interfaces.NAMING_LANGUAGE_ZH_CN
	}
	return d.namingLanguage
}

// tableName names an item drafted from a table in the naming language, with its aliases: from the
// name semantic understanding or a person gave it, then the first clause of its description, then
// its own, as fields are named. Semantic understanding writes a sentence as the description, whose
// first clause names badly.
func (d *drafter) tableName(table *interfaces.VegaTable) (string, []string) {
	return pickName(d.language(), physicalName(table), givenName(table), firstClause(table.Description))
}

// givenName is the name semantic understanding or a person gave a table, empty while it keeps the
// name discovery gave it: its physical name, which is no candidate anyway, or, for tables discovered
// before vega 0.2.1, its physical name with its schema, as vega itself reads a default name.
func givenName(table *interfaces.VegaTable) string {
	if table.Name == table.SourceSchema+"."+table.SourceName {
		return ""
	}
	return table.Name
}

// dropAliasesOfOtherProperties leaves out an alias that is another property's name or display name:
// BKN refuses a package with one.
func dropAliasesOfOtherProperties(properties []*interfaces.DraftProperty) {
	owner := map[string]*interfaces.DraftProperty{}
	for _, p := range properties {
		for _, name := range []string{p.Name, p.DisplayName} {
			if _, taken := owner[name]; !taken {
				owner[name] = p
			}
		}
	}
	for _, p := range properties {
		p.Aliases = slices.DeleteFunc(p.Aliases, func(alias string) bool {
			other, taken := owner[alias]
			return taken && other != p
		})
		if len(p.Aliases) == 0 {
			p.Aliases = nil
		}
	}
}

// draftLanguage is the naming language a draft named its items in; Chinese for a draft made before
// naming languages.
func draftLanguage(draft *interfaces.Draft) string {
	if draft.Items.Inputs == nil || draft.Items.Inputs.NamingLanguage == "" {
		return interfaces.NAMING_LANGUAGE_ZH_CN
	}
	return draft.Items.Inputs.NamingLanguage
}
