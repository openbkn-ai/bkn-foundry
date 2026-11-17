// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

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
		if c != name && nameLanguage(c) != nameLanguage(name) &&
			utf8.RuneCountInString(c) <= maxAliasRunes && !strings.ContainsAny(c, ",\uff0c") {
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

// messageRelationName is the localized template a relation drafted from a referring field is named
// by, in the naming language: a predicate read from its source to its target.
const messageRelationName = "BknProject.Draft.RelationLinks"

// messageRelationNameByTarget names such a relation when its field reads as its target's name, as a
// field 客户 that refers to 客户 does: the field tells nothing more.
const messageRelationNameByTarget = "BknProject.Draft.RelationLinksTarget"

// messageLegacyRelationName is the template drafting named such relations by before: the field's
// name with its object type's, which reads as a field, not a relation. Edits still recognise it.
const messageLegacyRelationName = "BknProject.Draft.RelationName"

// relationName names a relation drafted from a referring field, in the naming language: a predicate
// that reads as a sentence from its source to its target, with the field that joins them
// (物料BOM明细 「按父项物料编码关联物料」 物料, BOM detail "Links Material by Parent material code"
// Material), as the referring fields tell apart the relations between the same two types; without
// the field when it reads as the target's name (「关联客户」, "Links Customer"). When that
// is longer than BKN takes for a display name, the field's name alone. A field nobody named keeps an
// ASCII physical name such as customer_id, and joining technical names reads no better than the id,
// which also names the target: the relation keeps its id. A physical name that is not ASCII, such as
// 供应商编号, reads as written.
func (d *drafter) relationName(source *interfaces.DraftObjectType, target *interfaces.DraftObjectType,
	property string, id string) string {
	return relationNameIn(d.language(), source, target, property, id)
}

// relationNameIn is relationName in a given naming language.
func relationNameIn(language string, source *interfaces.DraftObjectType, target *interfaces.DraftObjectType,
	property string, id string) string {
	p := findProperty(source, property)
	if p == nil || target == nil || p.DisplayName == p.MappedField && isASCII(p.MappedField) {
		return id
	}
	message := messageRelationName
	if strings.EqualFold(strings.TrimSpace(p.DisplayName), strings.TrimSpace(target.Name)) {
		message = messageRelationNameByTarget
	}
	name, err := i18n.Render(i18n.Language(language), message,
		map[string]any{"target": target.Name, "field": p.DisplayName})
	if err != nil || utf8.RuneCountInString(name) > maxDisplayNameRunes {
		return p.DisplayName
	}
	return name
}

// messageRelationNameFrom names such a relation by its source as well, when another relation has
// its short name already: relation type names are unique in a network.
const messageRelationNameFrom = "BknProject.Draft.RelationLinksFrom"

// relationNameFromIn is relationNameIn with the source's name in front, for a relation whose short
// name another relation has.
func relationNameFromIn(language string, source *interfaces.DraftObjectType, target *interfaces.DraftObjectType,
	property string, id string) string {
	p := findProperty(source, property)
	if p == nil || target == nil || p.DisplayName == p.MappedField && isASCII(p.MappedField) {
		return id
	}
	name, err := i18n.Render(i18n.Language(language), messageRelationNameFrom,
		map[string]any{"source": source.Name, "target": target.Name, "field": p.DisplayName})
	if err != nil || utf8.RuneCountInString(name) > maxDisplayNameRunes {
		return relationNameIn(language, source, target, property, id)
	}
	return name
}

// relationNameTaken reports whether a relation of items other than id is named name.
func relationNameTaken(items []*interfaces.DraftRelationType, id string, name string) bool {
	for _, r := range items {
		if r != nil && r.ID != id && r.Name == name {
			return true
		}
	}
	return false
}

// relationNameInWithLimit is the name drafting gave such a relation before it named relations as
// predicates, under the display-name contract of a particular draft version. It lets edits recognise
// relation names generated before the contract grew from 40 to 128 characters.
func relationNameInWithLimit(language string, source *interfaces.DraftObjectType, property string, id string,
	maxRunes int) string {
	p := findProperty(source, property)
	if p == nil || p.DisplayName == p.MappedField && isASCII(p.MappedField) {
		return id
	}
	name, err := i18n.Render(i18n.Language(language), messageLegacyRelationName,
		map[string]any{"object_type": source.Name, "field": p.DisplayName})
	if err != nil || utf8.RuneCountInString(name) > maxRunes {
		return p.DisplayName
	}
	return name
}

// draftedRelationName tells whether a relation still bears a name drafting gave it from its source's
// referring field: the predicate it names relations by now, or the field-like name of earlier drafts.
func draftedRelationName(language string, source *interfaces.DraftObjectType, target *interfaces.DraftObjectType,
	property string, id string, name string) bool {
	return name == relationNameIn(language, source, target, property, id) ||
		name == relationNameFromIn(language, source, target, property, id) ||
		name == relationNameInWithLimit(language, source, property, id, maxDisplayNameRunes) ||
		name == relationNameInWithLimit(language, source, property, id, legacyRelationNameMaxRunes)
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
	return itemsLanguage(&draft.Items)
}

// itemsLanguage is the naming language draft items were named in.
func itemsLanguage(items *interfaces.DraftItems) string {
	if items.Inputs == nil || items.Inputs.NamingLanguage == "" {
		return interfaces.NAMING_LANGUAGE_ZH_CN
	}
	return items.Inputs.NamingLanguage
}

// renameRelations names again, after its object type is renamed to name, each relation drafted
// from a referring field that ends at it and still bears the name drafting gave it, now or in an
// earlier draft; a relation a person renamed stays, and one named by its id is named by it again.
// Each rename is recorded through decide.
func renameRelations(items *interfaces.DraftItems, objectType *interfaces.DraftObjectType, name string,
	decide func(kind string, id string, action string, before any, after any)) {
	language := itemsLanguage(items)
	renamed := *objectType
	renamed.Name = name
	for _, r := range items.RelationTypes {
		// Relations drafted from link tables map no field of the object type, and are named apart.
		if r.Source != objectType.ID && r.Target != objectType.ID || len(r.Mapping) == 0 {
			continue
		}
		source, target := findObjectType(items, r.Source), findObjectType(items, r.Target)
		property := r.Mapping[0].SourceProperty
		if source == nil || !draftedRelationName(language, source, target, property, r.ID, r.Name) {
			continue
		}
		qualified := r.Name != relationNameIn(language, source, target, property, r.ID) &&
			r.Name == relationNameFromIn(language, source, target, property, r.ID)
		if r.Source == objectType.ID {
			source = &renamed
		}
		if r.Target == objectType.ID {
			target = &renamed
		}
		again := relationNameIn(language, source, target, property, r.ID)
		if qualified || relationNameTaken(items.RelationTypes, r.ID, again) {
			again = relationNameFromIn(language, source, target, property, r.ID)
		}
		if again != r.Name {
			decide(interfaces.DRAFT_KIND_RELATION_TYPES, r.ID, interfaces.DRAFT_DECISION_RENAME, r.Name, again)
			r.Name = again
		}
	}
	renameTableNameIn(items, objectType, name, decide)
}

// renameTableNameIn writes name for an object type's table name in the names of the relations
// that end at it, once the object type, still named by its table, takes a business name: a
// relation named when its end had no other name, such as 物料对应hd_product_view, reads
// 物料对应产品 then. Only the table name as a whole word is replaced, and a relation a person
// renamed stays as named.
func renameTableNameIn(items *interfaces.DraftItems, objectType *interfaces.DraftObjectType, name string,
	decide func(kind string, id string, action string, before any, after any)) {
	old := objectType.Name
	if old == "" || old == name || old != objectType.Table.Name || !isASCII(old) {
		return
	}
	named := map[string]bool{}
	for _, d := range items.Decisions {
		if d.Kind == interfaces.DRAFT_KIND_RELATION_TYPES && d.Action == interfaces.DRAFT_DECISION_RENAME {
			named[d.ItemID] = true
		}
	}
	for _, r := range items.RelationTypes {
		if r.Existing || named[r.ID] || r.Name == r.ID || (r.Source != objectType.ID && r.Target != objectType.ID) {
			continue
		}
		again := replaceWord(r.Name, old, name)
		if again == r.Name || utf8.RuneCountInString(again) > maxDisplayNameRunes {
			continue
		}
		decide(interfaces.DRAFT_KIND_RELATION_TYPES, r.ID, interfaces.DRAFT_DECISION_RENAME, r.Name, again)
		r.Name = again
	}
}

// replaceWord replaces each occurrence of word in text that no letter, digit or underscore of an
// ASCII name continues on either side.
func replaceWord(text string, word string, with string) string {
	part := func(b byte) bool {
		return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
	}
	var out strings.Builder
	for {
		at := strings.Index(text, word)
		if at < 0 {
			out.WriteString(text)
			return out.String()
		}
		end := at + len(word)
		if (at > 0 && part(text[at-1])) || (end < len(text) && part(text[end])) {
			out.WriteString(text[:end])
		} else {
			out.WriteString(text[:at])
			out.WriteString(with)
		}
		text = text[end:]
	}
}
