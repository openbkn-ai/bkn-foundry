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
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/locale"
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

// roleOfField is the role a referring field names, its display name without a trailing code word:
// 父项物料编码 → 父项物料, 客户ID → 客户, Supplier No. → Supplier, CustomerID → Customer. An English
// code word ends the role only after a space, underscore or hyphen, a Han character, or a lower-case
// letter when it starts in upper case, so Barcode and Monkey keep their ending. Empty when nothing
// that names a role is left: no letter, or a single Han character, as 父 of 父编码 or 工 of 工号.
func roleOfField(field string) string {
	field = strings.TrimSpace(field)
	for _, word := range locale.RelationCodeWords {
		at := len(field) - len(word)
		if at < 0 || !utf8.RuneStart(field[at]) || !strings.EqualFold(field[at:], word) {
			continue
		}
		if at == 0 {
			return ""
		}
		before, _ := utf8.DecodeLastRuneInString(field[:at])
		first, _ := utf8.DecodeRuneInString(field[at:])
		ascii := first < utf8.RuneSelf
		if ascii && !(before == ' ' || before == '_' || before == '-' || unicode.Is(unicode.Han, before) ||
			unicode.IsLower(before) && unicode.IsUpper(first)) {
			continue
		}
		role := strings.TrimRight(field[:at], " _-")
		runes := []rune(role)
		if len(runes) == 1 && unicode.Is(unicode.Han, runes[0]) || !strings.ContainsFunc(role, unicode.IsLetter) {
			return ""
		}
		return role
	}
	return field
}

// relationName names a relation drafted from a referring field as a link type is named: by the role
// its target plays for its source, read from the source. The role is the field's display name
// without a trailing code word (物料BOM明细 —父项物料→ 物料, from 父项物料编码; 销售订单 —下单客户→
// 客户); the target's name when nothing is left or the field reads as the target's name (供应商编号
// → 供应商, Customer ID → Customer). The source and the joining field are not in the name: they are
// the mapping. A field nobody named keeps an ASCII physical name such as customer_id, which names
// the target no better than the id: the relation keeps its id. A physical name that is not ASCII,
// such as 供应商编号, reads as written.
func (d *drafter) relationName(source *interfaces.DraftObjectType, target *interfaces.DraftObjectType,
	property string, id string) string {
	return relationNameIn(d.language(), source, target, property, id)
}

// relationNameIn is relationName in a given naming language. The role name reads in the field's
// language; the naming language only matters for the names earlier drafts gave.
func relationNameIn(_ string, source *interfaces.DraftObjectType, target *interfaces.DraftObjectType,
	property string, id string) string {
	p := findProperty(source, property)
	if p == nil || target == nil || p.DisplayName == p.MappedField && isASCII(p.MappedField) {
		return id
	}
	role := roleOfField(p.DisplayName)
	if role == "" || strings.EqualFold(role, strings.TrimSpace(target.Name)) ||
		strings.EqualFold(strings.TrimSpace(p.DisplayName), strings.TrimSpace(target.Name)) {
		role = strings.TrimSpace(target.Name)
	}
	if role == "" || utf8.RuneCountInString(role) > maxDisplayNameRunes {
		return p.DisplayName
	}
	return role
}

// relationNameFullIn names such a relation by the field's whole display name, when another relation
// of the same source has its role name already: 父项物料编码 and 子项物料编码 both give 物料 once
// their roles are taken. A relation's name is unique among the relations of its source only; it may
// equal an object type's name or the name of another source's relation.
func relationNameFullIn(source *interfaces.DraftObjectType, target *interfaces.DraftObjectType, property string,
	id string) string {
	p := findProperty(source, property)
	if p == nil || target == nil || p.DisplayName == p.MappedField && isASCII(p.MappedField) {
		return id
	}
	return p.DisplayName
}

// relationNameTaken reports whether a relation of items other than id, from source, is named name.
func relationNameTaken(items []*interfaces.DraftRelationType, source string, id string, name string) bool {
	for _, r := range items {
		if r != nil && r.ID != id && r.Source == source && r.Name == name {
			return true
		}
	}
	return false
}

// The templates earlier drafts named such relations by. Edits and translations still recognise them,
// so a relation that bears one follows its ends as one named now does.
const (
	// messageLegacyRelationLinks is 「按{字段}关联{目标}」, "Links {target} by {field}".
	messageLegacyRelationLinks = "BknProject.Draft.RelationLinks"
	// messageLegacyRelationLinksTarget is 「关联{目标}」, "Links {target}", for a field that reads as
	// the target's name.
	messageLegacyRelationLinksTarget = "BknProject.Draft.RelationLinksTarget"
	// messageLegacyRelationLinksFrom is 「{来源}按{字段}关联{目标}」, for a relation whose short name
	// was taken.
	messageLegacyRelationLinksFrom = "BknProject.Draft.RelationLinksFrom"
	// messageLegacyRelationName is 「{对象}的{字段}」, "{field} of {object}", which reads as a field.
	messageLegacyRelationName = "BknProject.Draft.RelationName"
)

// legacyRelationNamesIn are the names earlier drafts gave a relation drafted from a referring field:
// the predicates 「按{字段}关联{目标}」 or 「关联{目标}」, and 「{来源}按{字段}关联{目标}」; and the
// field-like 「{对象}的{字段}」 under the 128- and the former 40-character display-name limits, which
// fell back to the field's name when longer.
func legacyRelationNamesIn(language string, source *interfaces.DraftObjectType, target *interfaces.DraftObjectType,
	property string) []string {
	p := findProperty(source, property)
	if p == nil || p.DisplayName == p.MappedField && isASCII(p.MappedField) {
		return nil
	}
	render := func(message string, values map[string]any, maxRunes int) string {
		name, err := i18n.Render(i18n.Language(language), message, values)
		if err != nil || utf8.RuneCountInString(name) > maxRunes {
			return p.DisplayName
		}
		return name
	}
	names := []string{
		render(messageLegacyRelationName, map[string]any{"object_type": source.Name, "field": p.DisplayName}, maxDisplayNameRunes),
		render(messageLegacyRelationName, map[string]any{"object_type": source.Name, "field": p.DisplayName}, legacyRelationNameMaxRunes),
	}
	if target != nil {
		values := map[string]any{"source": source.Name, "target": target.Name, "field": p.DisplayName}
		message := messageLegacyRelationLinks
		if strings.EqualFold(strings.TrimSpace(p.DisplayName), strings.TrimSpace(target.Name)) {
			message = messageLegacyRelationLinksTarget
		}
		names = append(names, render(message, values, maxDisplayNameRunes),
			render(messageLegacyRelationLinksFrom, values, maxDisplayNameRunes))
	}
	return names
}

// draftedRelationName tells whether a relation still bears a name drafting gave it from its source's
// referring field: the role it names relations by now, the field's whole name a clash gives, or a
// name of earlier drafts.
func draftedRelationName(language string, source *interfaces.DraftObjectType, target *interfaces.DraftObjectType,
	property string, id string, name string) bool {
	return name == relationNameIn(language, source, target, property, id) ||
		name == relationNameFullIn(source, target, property, id) ||
		slices.Contains(legacyRelationNamesIn(language, source, target, property), name)
}

// relationNameAmong names a relation drafted from a referring field among relations: by its role,
// or by the field's whole name when another relation of the same source has the role name, or by its
// id when that one is taken too.
func relationNameAmong(language string, relations []*interfaces.DraftRelationType, source *interfaces.DraftObjectType,
	target *interfaces.DraftObjectType, property string, id string) string {
	name := relationNameIn(language, source, target, property, id)
	if !relationNameTaken(relations, source.ID, id, name) {
		return name
	}
	if full := relationNameFullIn(source, target, property, id); !relationNameTaken(relations, source.ID, id, full) {
		return full
	}
	// Another field of the source is named like this one's role: the id keeps the two apart.
	return id
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
// A role name changes only when it is its target's name; a name of an earlier draft, or the field's
// whole name a clash gave, takes the role unless another relation of the source has it.
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
		if r.Source == objectType.ID {
			source = &renamed
		}
		if r.Target == objectType.ID {
			target = &renamed
		}
		again := relationNameAmong(language, items.RelationTypes, source, target, property, r.ID)
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
