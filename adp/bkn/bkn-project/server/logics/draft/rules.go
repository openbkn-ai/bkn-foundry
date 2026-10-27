// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// Rules of drafting that read only table structure: how a table is classified, how its key
// is found, how names and types carry over to BKN. The counts that check keys and relations
// come from a counter (stats.go).

const (
	maxIDLength         = 40
	maxDisplayNameRunes = 40
	tableKindEntity     = "entity"
	tableKindCodeTable  = "code_table"
	tableKindLinkTable  = "link_table"
	excludedPrivacy     = "privacy"
	excludedUnsupported = "unsupported_type"
)

// bknTypes maps vega's field types to BKN data types; types missing here are not mapped.
var bknTypes = map[string]string{
	"string":           "string",
	"text":             "text",
	"integer":          "integer",
	"unsigned integer": "unsigned integer",
	"large_integer":    "integer",
	"float":            "float",
	"decimal":          "decimal",
	"boolean":          "boolean",
	"date":             "date",
	"time":             "time",
	"datetime":         "datetime",
	"timestamp":        "timestamp",
	"json":             "json",
	"binary":           "binary",
	"ip":               "ip",
	"point":            "point",
	"shape":            "shape",
	"vector":           "vector",
}

// keyTypes are the BKN data types a primary key may have.
var keyTypes = map[string]bool{"string": true, "text": true, "integer": true, "unsigned integer": true}

var nonIDChars = regexp.MustCompile(`[^a-z0-9_]+`)

// clauseEnds end the first clause of a comment: ASCII . ; , : and newline, and the CJK full
// stop, semicolon, comma and colon (U+3002, U+FF1B, U+FF0C, U+FF1A), which comments in Chinese use.
const clauseEnds = ".;,:\n\u3002\uff1b\uff0c\uff1a"

// apiName turns a table or field name into a BKN id: lowercase letters, digits and
// underscores, not starting with an underscore or digit, at most 40 characters.
func apiName(name string) string {
	id := strings.Trim(nonIDChars.ReplaceAllString(strings.ToLower(name), "_"), "_")
	if id == "" {
		id = "field"
	}
	if id[0] >= '0' && id[0] <= '9' {
		id = "f_" + id
	}
	if len(id) > maxIDLength {
		id = strings.TrimRight(id[:maxIDLength], "_")
	}
	return id
}

// uniqueName returns name, or name with the first free numeric suffix, and takes it.
func uniqueName(name string, taken map[string]bool) string {
	candidate := name
	for i := 2; taken[candidate]; i++ {
		suffix := fmt.Sprintf("_%d", i)
		base := name
		if len(base)+len(suffix) > maxIDLength {
			base = strings.TrimRight(base[:maxIDLength-len(suffix)], "_")
		}
		candidate = base + suffix
	}
	taken[candidate] = true
	return candidate
}

// displayName is the first clause of a comment when it is short enough, else the name.
func displayName(comment string, name string) string {
	clause := strings.TrimSpace(comment)
	if i := strings.IndexAny(clause, clauseEnds); i >= 0 {
		clause = strings.TrimSpace(clause[:i])
	}
	if clause == "" || utf8.RuneCountInString(clause) > maxDisplayNameRunes {
		return name
	}
	return clause
}

// words splits a name into lowercase words at non-alphanumerics and camelCase boundaries:
// contactEmail and contact_email both give [contact email].
func words(name string) []string {
	out := []string{}
	var word []rune
	flush := func() {
		if len(word) > 0 {
			out = append(out, strings.ToLower(string(word)))
			word = word[:0]
		}
	}
	var prev rune
	for _, r := range name {
		switch {
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			flush()
		case unicode.IsUpper(r) && unicode.IsLower(prev):
			flush()
			word = append(word, r)
		default:
			word = append(word, r)
		}
		prev = r
	}
	flush()
	return out
}

// containsWords reports whether want appears in have as consecutive words.
func containsWords(have []string, want []string) bool {
	if len(want) == 0 {
		return false
	}
	for i := 0; i+len(want) <= len(have); i++ {
		match := true
		for j, w := range want {
			if have[i+j] != w {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// isASCII reports whether a keyword is written in Latin letters, which form words.
func isASCII(s string) bool {
	for _, r := range s {
		if r > unicode.MaxASCII {
			return false
		}
	}
	return true
}

// privacyKeyword returns the keyword a field's name or description matches, or "". A Latin
// keyword matches whole words (id_number matches id_number, not materialplanid_number); a
// Chinese one, which has no word breaks, matches as a substring.
func privacyKeyword(column interfaces.VegaColumn, keywords []string) string {
	nameWords, descriptionWords := words(column.Name), words(column.Description)
	text := strings.ToLower(column.Name + " " + column.Description)
	for _, keyword := range keywords {
		if keyword == "" {
			continue
		}
		if isASCII(keyword) {
			k := words(keyword)
			if containsWords(nameWords, k) || containsWords(descriptionWords, k) {
				return keyword
			}
		} else if strings.Contains(text, strings.ToLower(keyword)) {
			return keyword
		}
	}
	return ""
}

// isNameField reports whether a field holds a record's name: its name is, or ends with, a keyword.
func isNameField(column interfaces.VegaColumn, keywords []string) bool {
	name := strings.ToLower(column.Name)
	for _, keyword := range keywords {
		if k := strings.ToLower(keyword); k != "" && strings.HasSuffix(name, k) {
			return true
		}
	}
	return false
}

// tableKey returns the fields of the table's primary key, else of its first unique index.
func tableKey(table *interfaces.VegaTable) ([]string, string) {
	if len(table.PrimaryKeys) > 0 {
		return table.PrimaryKeys, interfaces.DRAFT_KEY_PRIMARY_KEY
	}
	for _, index := range table.Indices {
		if index.Primary && len(index.Columns) > 0 {
			return index.Columns, interfaces.DRAFT_KEY_PRIMARY_KEY
		}
	}
	for _, index := range table.Indices {
		if index.Unique && len(index.Columns) > 0 {
			return index.Columns, interfaces.DRAFT_KEY_UNIQUE_INDEX
		}
	}
	return nil, interfaces.DRAFT_KEY_NONE
}

// isUniqueField reports whether a single field alone is a key or a unique index of the table.
func isUniqueField(table *interfaces.VegaTable, field string) bool {
	if len(table.PrimaryKeys) == 1 && table.PrimaryKeys[0] == field {
		return true
	}
	for _, index := range table.Indices {
		if (index.Unique || index.Primary) && len(index.Columns) == 1 && index.Columns[0] == field {
			return true
		}
	}
	return false
}

func column(table *interfaces.VegaTable, name string) (interfaces.VegaColumn, bool) {
	for _, c := range table.Columns {
		if c.Name == name {
			return c, true
		}
	}
	return interfaces.VegaColumn{}, false
}

// codeWords end the names of codes and of the tables that list them.
var codeWords = map[string]bool{
	"code": true, "codes": true, "type": true, "types": true, "status": true, "statuses": true,
	"level": true, "levels": true, "category": true, "categories": true, "kind": true, "kinds": true,
	"class": true, "classes": true, "dict": true, "dictionary": true,
}

func lastWord(w []string) string {
	if len(w) == 0 {
		return ""
	}
	return w[len(w)-1]
}

func looksLikeCode(name string) bool {
	return codeWords[lastWord(words(name))]
}

// identityCandidates are fields, in order, that a table without a declared key is likely
// identified by: id, <table>_id, then a first field named like an identity.
func identityCandidates(table *interfaces.VegaTable) []string {
	candidates := []string{}
	seen := map[string]bool{}
	add := func(field string) {
		c, ok := column(table, field)
		if ok && !seen[field] && keyTypes[bknTypes[c.Type]] {
			seen[field] = true
			candidates = append(candidates, field)
		}
	}
	tableID := apiName(table.Name) + "_id"
	for _, c := range table.Columns {
		if f := apiName(c.Name); f == "id" || f == tableID {
			add(c.Name)
		}
	}
	if len(table.Columns) > 0 {
		first := table.Columns[0].Name
		w := words(first)
		if last := lastWord(w); last == "id" || last == "code" || last == "no" ||
			strings.EqualFold(first, "billno") || strings.EqualFold(first, "bill_no") {
			add(first)
		}
	}
	return candidates
}

// codeTableName returns the field holding the names of a code table, or "" when the table is
// not one: a single-field key, a known row count up to the limit, a few other fields and one
// of them a name. A table with a personal field is never one, so drafting never reads people's
// names as enum values.
func codeTableName(table *interfaces.VegaTable, setting common.DraftSetting) string {
	key, _ := tableKey(table)
	if len(key) != 1 || table.Rows <= 0 || table.Rows > setting.CodeTableMaxRows {
		return ""
	}
	// A dictionary is keyed by a code, or named for one: organizations(organization_id, name,
	// role, role_code) is a small entity, not a dictionary.
	if !looksLikeCode(key[0]) && !looksLikeCode(table.Name) {
		return ""
	}
	for _, c := range table.Columns {
		if privacyKeyword(c, setting.PrivacyKeywords) != "" {
			return ""
		}
	}
	others := []interfaces.VegaColumn{}
	for _, c := range table.Columns {
		if c.Name != key[0] {
			others = append(others, c)
		}
	}
	if len(others) == 0 || len(others) > setting.CodeTableMaxColumns {
		return ""
	}
	for _, c := range others {
		if isNameField(c, setting.NameFieldKeywords) && bknTypes[c.Type] != "" {
			return c.Name
		}
	}
	return ""
}

// isLinkTable reports whether a table only joins two others: exactly two single-field foreign
// keys and at most two fields besides them and its key.
func isLinkTable(table *interfaces.VegaTable) bool {
	if len(table.ForeignKeys) != 2 {
		return false
	}
	used := map[string]bool{}
	for _, fk := range table.ForeignKeys {
		if len(fk.Columns) != 1 || len(fk.RefColumns) != 1 {
			return false
		}
		used[fk.Columns[0]] = true
	}
	key, _ := tableKey(table)
	for _, field := range key {
		used[field] = true
	}
	extra := 0
	for _, c := range table.Columns {
		if !used[c.Name] {
			extra++
		}
	}
	return extra <= 2
}

func classify(table *interfaces.VegaTable, setting common.DraftSetting) string {
	switch {
	case isLinkTable(table):
		return tableKindLinkTable
	case codeTableName(table, setting) != "":
		return tableKindCodeTable
	default:
		return tableKindEntity
	}
}
