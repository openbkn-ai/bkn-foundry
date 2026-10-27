// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"fmt"
	"regexp"
	"strings"
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

// nameFields are field names, or the end of them, that hold a record's name.
var nameFields = []string{"name", "label", "title", "名称", "名字"}

var nonIDChars = regexp.MustCompile(`[^a-z0-9_]+`)

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
	if i := strings.IndexAny(clause, "。.;；,，:：\n"); i >= 0 {
		clause = strings.TrimSpace(clause[:i])
	}
	if clause == "" || utf8.RuneCountInString(clause) > maxDisplayNameRunes {
		return name
	}
	return clause
}

// privacyKeyword returns the keyword a field's name or comment matches, or "".
func privacyKeyword(column interfaces.VegaColumn, keywords []string) string {
	name := strings.ToLower(column.Name)
	comment := strings.ToLower(column.Description)
	for _, keyword := range keywords {
		k := strings.ToLower(keyword)
		if k != "" && (strings.Contains(name, k) || strings.Contains(comment, k)) {
			return keyword
		}
	}
	return ""
}

func isNameField(column interfaces.VegaColumn) bool {
	name := strings.ToLower(column.Name)
	for _, field := range nameFields {
		if name == field || strings.HasSuffix(name, "_"+field) || strings.HasSuffix(name, field) {
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

// codeTableName returns the field holding the names of a code table, or "" when the table is
// not one: a single-field key, a known row count up to the limit, a few other fields and one
// of them a name.
func codeTableName(table *interfaces.VegaTable, setting common.DraftSetting) string {
	key, _ := tableKey(table)
	if len(key) != 1 || table.Rows <= 0 || table.Rows > setting.CodeTableMaxRows {
		return ""
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
		if isNameField(c) && bknTypes[c.Type] != "" {
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
