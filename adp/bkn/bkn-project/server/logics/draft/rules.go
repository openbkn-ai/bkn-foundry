// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
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

// Prefixes of the ids made for physical names that are not ASCII, by the kind of item.
const (
	idPrefixObjectType   = "ot"
	idPrefixProperty     = "p"
	idPrefixValueType    = "vt"
	idPrefixRelationType = "rt"
)

// idFrom makes the BKN id of an item from the physical name it is drafted from: its words, camelCase
// split, joined by "_". BKN ids are ASCII, so a name that is not stands as the kind's prefix and a
// short hash of the name: the same name always gives the same id, whoever drafts it and in whatever
// language. The name people read is the item's name, not its id.
func idFrom(name string, prefix string) string {
	if !isASCII(name) {
		sum := sha256.Sum256([]byte(name))
		return prefix + "_" + hex.EncodeToString(sum[:])[:6]
	}
	return apiName(strings.Join(words(name), "_"))
}

// physicalName is the name of the table in its source. A resource's name is what people read, and
// semantic understanding may rewrite it; ids and name matching read the source's name, which does
// not change with it.
func physicalName(table *interfaces.VegaTable) string {
	if table.SourceName != "" {
		return table.SourceName
	}
	return table.Name
}

// matchName is a name as name matching compares it: its words joined by "_", letters of any script
// kept, so fields named in Chinese match as written.
func matchName(name string) string {
	return strings.Join(words(name), "_")
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

// words splits a name into lowercase words at non-alphanumerics, camelCase boundaries and where
// the script changes: contactEmail and contact_email both give [contact email], 客户ID [客户 id].
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
		case unicode.IsUpper(r) && unicode.IsLower(prev),
			len(word) > 0 && (r > unicode.MaxASCII) != (prev > unicode.MaxASCII):
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

// isASCII reports whether a keyword is written in Latin letters, which form words.
func isASCII(s string) bool {
	for _, r := range s {
		if r > unicode.MaxASCII {
			return false
		}
	}
	return true
}

// wordMatches reports whether keyword words kw occur in a field's words: consecutive, the
// inner ones whole, the first allowed to end a longer word (cellphone) and the last to begin
// one (birthday, phone2). A one-word keyword may sit anywhere in a word that way, but a
// longer keyword never starts inside a word: id_number does not match materialplanid_number.
func wordMatches(have []string, kw []string) bool {
	n := len(kw)
	if n == 0 {
		return false
	}
	for i := 0; i+n <= len(have); i++ {
		match := true
		for j, k := range kw {
			w := have[i+j]
			switch {
			case n == 1:
				match = strings.HasPrefix(w, k) || strings.HasSuffix(w, k)
			case j == 0:
				match = w == k
			case j == n-1:
				match = strings.HasPrefix(w, k)
			default:
				match = w == k
			}
			if !match {
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// privacyKeyword returns the keyword a field's name, description or database comment matches,
// or "". Latin keywords match by words (see wordMatches); Chinese ones, which have no word
// breaks, match as substrings.
func privacyKeyword(column interfaces.VegaColumn, keywords []string) string {
	texts := []string{column.Name, column.Description, column.Comment}
	for _, keyword := range keywords {
		if keyword == "" {
			continue
		}
		for _, text := range texts {
			if isASCII(keyword) {
				if wordMatches(words(text), words(keyword)) {
					return keyword
				}
			} else if strings.Contains(strings.ToLower(text), strings.ToLower(keyword)) {
				return keyword
			}
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

// minBusinessStemRunes is the shortest business-name stem that may point at a table.
const minBusinessStemRunes = 2

// fieldStem is a field's name without the identifier word it ends in, as words: [supplier] for
// supplier_number. Nil when it ends in none or what is left is too common to point at a table.
func fieldStem(name string, setting common.DraftSetting) []string {
	w := words(name)
	if len(w) < 2 || !slices.Contains(setting.IdentifierWords, lastWord(w)) {
		return nil
	}
	stem := w[:len(w)-1]
	if joined := strings.Join(stem, "_"); slices.Contains(setting.GenericStems, joined) || genericKeys[joined] {
		return nil
	}
	return stem
}

// stemMatch ranks how a field's stem points at another table, 0 when it does not: the table's
// name ending in the stem (supplier_number to erp_supplier) above the key's stem being the same
// (supplier_number to a key supplier_code).
func stemMatch(field string, other *interfaces.VegaTable, key string, setting common.DraftSetting) int {
	stem := fieldStem(field, setting)
	if stem == nil {
		return 0
	}
	switch table := words(physicalName(other)); {
	case len(table) >= len(stem) && slices.Equal(table[len(table)-len(stem):], stem):
		return 3
	case slices.Equal(fieldStem(key, setting), stem):
		return 2
	}
	return 0
}

// businessStem is a field's business name without the identifier it ends in: 供应商 for
// 供应商编号, supplier for Supplier number. Empty when it ends in none, or what is left is too
// short or too common to point at a table.
func businessStem(name string, setting common.DraftSetting) string {
	name = strings.TrimSpace(name)
	stem := ""
	for _, word := range setting.IdentifierWords {
		if isASCII(word) {
			if w := words(name); len(w) >= 2 && lastWord(w) == word {
				stem = strings.Join(w[:len(w)-1], " ")
			}
		} else if s, ok := strings.CutSuffix(name, word); ok {
			stem = s
		}
		if stem != "" {
			break
		}
	}
	stem = strings.ToLower(strings.TrimSpace(stem))
	if utf8.RuneCountInString(stem) < minBusinessStemRunes || slices.Contains(setting.GenericStems, stem) {
		return ""
	}
	return stem
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
	tableID := matchName(physicalName(table)) + "_id"
	for _, c := range table.Columns {
		if f := matchName(c.Name); f == "id" || f == tableID {
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
