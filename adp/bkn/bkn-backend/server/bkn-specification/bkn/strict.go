// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// A 3.0 package has exactly one way to write each thing (SPECIFICATION.md §1.3). The 2.x reader
// accepts synonyms and silently drops what it does not know, so a mistake neither fails nor takes
// effect. The checks in this file run before a 3.0 definition is parsed and refuse every such
// form with a message that names the place and the one accepted spelling. The 2.x reader never
// calls them.

// Definition kinds, spelled as the frontmatter type of each subdirectory.
const (
	kindNetwork      = "knowledge_network"
	kindObjectType   = "object_type"
	kindRelationType = "relation_type"
	kindActionType   = "action_type"
	kindRiskType     = "risk_type"
	kindConceptGroup = "concept_group"
	kindValueType    = "value_type"
)

// headingPrefix is the ## definition heading prefix of each kind.
var headingPrefix = map[string]string{
	kindObjectType:   "ObjectType",
	kindRelationType: "RelationType",
	kindActionType:   "ActionType",
	kindRiskType:     "RiskType",
	kindConceptGroup: "ConceptGroup",
	kindValueType:    "ValueType",
}

// frontmatterRule lists the frontmatter fields of a kind. Renamed fields are refused with the field
// to write instead.
type frontmatterRule struct {
	fields  []string
	renamed map[string]string
}

var frontmatterRules = map[string]frontmatterRule{
	kindNetwork:    {fields: []string{"type", "id", "name", "tags", "bkn_spec", "version", "branch", "capabilities"}},
	kindObjectType: {fields: []string{"type", "id", "name", "tags", "aliases", "lifecycle"}},
	kindRelationType: {fields: []string{
		"type", "id", "name", "tags", "aliases", "reverse_name", "importance", "fallback_for", "verification", "lifecycle",
	}},
	kindActionType: {
		fields:  []string{"type", "id", "name", "tags", "aliases", "action_intent", "lifecycle"},
		renamed: map[string]string{"action_type": "action_intent"},
	},
	kindRiskType:     {fields: []string{"type", "id", "name", "tags"}},
	kindConceptGroup: {fields: []string{"type", "id", "name", "tags"}},
	kindValueType:    {fields: []string{"type", "id", "name", "tags"}},
}

type contentKind int

const (
	contentTable contentKind = iota
	contentYAML
	contentKeys
	contentLogic
)

// tableRule describes the one table a section or a logic property label may hold.
type tableRule struct {
	columns   []string
	renamed   map[string]string // a column 2.x accepts as a synonym -> the 3.0 spelling
	refused   map[string]string // a column 2.x reads that 3.0 has no place for -> what to write
	singleRow bool
	// legacyLayout marks the action parameter table, which 2.x also reads in the legacy layout
	// Parameter | Source | Binding.
	legacyLayout bool
}

type sectionRule struct {
	content contentKind
	table   tableRule
}

var parameterTableRule = tableRule{
	columns:      []string{"Name", "Type", "Source", "Operation", "ValueFrom", "Value", "Description"},
	renamed:      map[string]string{"Parameter": "Name", "Value From": "ValueFrom"},
	refused:      map[string]string{"Binding": "ValueFrom and Value"},
	legacyLayout: true,
}

// dataPropertiesTableRule is the Data Properties table of an object type and of an indirect relation.
var dataPropertiesTableRule = tableRule{
	columns: []string{"Name", "Display Name", "Type", "Semantic", "Required", "Aliases", "Description", "Mapped Field", "Mask Rule"},
}

var sectionRules = map[string]map[string]sectionRule{
	kindObjectType: {
		"Data Source":          {content: contentTable, table: tableRule{columns: []string{"Type", "ID", "Name"}, singleRow: true}},
		"Data Properties":      {content: contentTable, table: dataPropertiesTableRule},
		"Property Constraints": {content: contentYAML},
		"Logic Properties":     {content: contentLogic},
		"Keys":                 {content: contentKeys},
	},
	kindRelationType: {
		"Endpoint": {content: contentTable, table: tableRule{
			columns:   []string{"Source", "Target", "Type", "Cardinality"},
			singleRow: true,
		}},
		"Relation Constraints": {content: contentYAML},
		"Data Properties":      {content: contentTable, table: dataPropertiesTableRule},
		"Property Constraints": {content: contentYAML},
		"Mapping Rules":        {content: contentTable, table: tableRule{columns: []string{"Source Property", "Target Property"}}},
		"Backing Resource":     {content: contentTable, table: tableRule{columns: []string{"Type", "ID"}, singleRow: true}},
		"Source Mapping":       {content: contentTable, table: tableRule{columns: []string{"Source Property", "Resource Property"}}},
		"Target Mapping":       {content: contentTable, table: tableRule{columns: []string{"Resource Property", "Target Property"}}},
		"Source Condition":     {content: contentYAML},
		"Target Condition":     {content: contentYAML},
	},
	kindActionType: {
		"Bound Object": {content: contentTable, table: tableRule{
			columns:   []string{"Bound Object"},
			refused:   map[string]string{"Action Type": "the frontmatter field action_intent"},
			singleRow: true,
		}},
		"Affect Object":     {content: contentTable, table: tableRule{columns: []string{"Affect Object", "Affect Description"}}},
		"Impact Contracts":  {content: contentYAML},
		"Trigger Condition": {content: contentYAML},
		"Action Source": {content: contentTable, table: tableRule{
			columns: []string{"Type", "BoxID", "ToolID", "McpID", "ToolName"},
			renamed: map[string]string{
				"Box ID": "BoxID", "Toolbox ID": "BoxID", "Tool ID": "ToolID",
				"MCP ID": "McpID", "Mcp ID": "McpID", "Tool Name": "ToolName",
			},
			singleRow: true,
		}},
		"Parameter Binding": {content: contentTable, table: parameterTableRule},
		"Schedule":          {content: contentTable, table: tableRule{columns: []string{"Type", "Expression"}, singleRow: true}},
	},
	kindConceptGroup: {
		"Object Types": {content: contentTable, table: tableRule{columns: []string{"ID", "Name", "Description"}}},
	},
	kindRiskType:  {},
	kindValueType: {"Definition": {content: contentYAML}},
}

// renamedSections are 2.x section titles 3.0 refuses, with the title to write instead.
var renamedSections = map[string]map[string]string{
	kindActionType: {"Tool Configuration": "Action Source"},
}

// relationMappingSections lists the mapping sections each relation Type uses.
var relationMappingSections = map[string][]string{
	RELATION_MAPPING_TYPE_DIRECT:              {"Mapping Rules"},
	RELATION_MAPPING_TYPE_INDIRECT:            {"Backing Resource", "Source Mapping", "Target Mapping"},
	RELATION_MAPPING_TYPE_FILTERED_CROSS_JOIN: {"Source Condition", "Target Condition"},
}

// logicLabelRules are the tables under the bold labels of a logic property.
var logicLabelRules = map[string]tableRule{
	"Meta": {columns: []string{"Display Name", "Type", "Description"}},
	"Source": {
		columns: []string{"Source Type", "Source ID", "Source Name", "BoxID", "ToolID", "ResultPath"},
		renamed: map[string]string{"Box ID": "BoxID", "Toolbox ID": "BoxID", "Tool ID": "ToolID", "Result Path": "ResultPath"},
	},
	"Parameters": {
		columns: parameterTableRule.columns,
		renamed: parameterTableRule.renamed,
		refused: parameterTableRule.refused,
	},
	"Analysis Dimensions": {columns: []string{"Name", "Display Name", "Type", "Description"}},
}

var keyLinePrefixes = []string{"Primary Keys:", "Display Key:", "Incremental Key:"}

// generatedNetworkSections are written by the exporter into network.bkn and ignored on import, so an
// exported package imports as is.
var generatedNetworkSections = map[string]bool{"Network Overview": true, "Directory Structure": true}

// conditionWrapperKeys wrap a condition in 2.x; 3.0 writes the condition directly.
var conditionWrapperKeys = []string{"condition", "trigger_condition"}

// checkFrontmatterV3 applies the 3.0 frontmatter rules of a kind to the parsed frontmatter.
func checkFrontmatterV3(kind string, fm rawFrontmatter) error {
	rule := frontmatterRules[kind]
	for _, key := range fm.keys {
		switch {
		case containsString(rule.fields, key):
		case rule.renamed[key] != "":
			return specErrorf("frontmatter field %q is not a 3.0 field; write %q", key, rule.renamed[key])
		default:
			return specErrorf("frontmatter field %q is not a %s field; the fields are %s",
				key, kind, strings.Join(rule.fields, ", "))
		}
	}
	if typ := fm.str("type"); typ != kind {
		return specErrorf("frontmatter type %q does not match the directory; write %q", typ, kind)
	}
	for _, key := range []string{"tags", "aliases"} {
		if node, ok := fm.values[key]; ok && node.Kind != yaml.SequenceNode {
			return specErrorf("frontmatter %s must be a YAML list such as [a, b]", key)
		}
	}
	return nil
}

// checkDefinitionV3 applies the 3.0 rules to the body of a definition file: the ## heading, the
// description and every ### section.
func checkDefinitionV3(kind, text string, fm rawFrontmatter) error {
	if err := checkFrontmatterV3(kind, fm); err != nil {
		return err
	}
	_, body := splitFrontmatter(text)
	loc := h2HeadingRE.FindStringSubmatchIndex(body)
	if loc == nil {
		return specErrorf("missing the definition heading; write \"## %s: %s\"", headingPrefix[kind], fm.str("name"))
	}
	heading := strings.TrimSpace(body[loc[2]:loc[3]])
	want := headingPrefix[kind] + ": " + fm.str("name")
	if heading != want {
		return specErrorf("definition heading %q does not match; write \"## %s\"", "## "+heading, want)
	}
	desc, sections, order := extractSectionsWithDesc(text)
	// A section is kept by its title, so a second one with the same title would replace the first.
	seen := map[string]bool{}
	for _, title := range order {
		if seen[title] {
			return specErrorf("section %q appears twice; write it once", "### "+title)
		}
		seen[title] = true
	}
	if kind == kindRiskType {
		// A risk type has no known sections: its whole body is description, ### headings included.
		return nil
	}
	if err := checkDescriptionV3(desc); err != nil {
		return err
	}
	return checkSectionsV3(kind, sections, order)
}

// checkNetworkV3 applies the 3.0 rules to network.bkn.
func checkNetworkV3(text string, fm rawFrontmatter) error {
	if err := checkFrontmatterV3(kindNetwork, fm); err != nil {
		return err
	}
	_, body := splitFrontmatter(text)
	if err := checkDescriptionV3(extractBodyDescription(text)); err != nil {
		return err
	}
	for _, m := range h2HeadingRE.FindAllStringSubmatch(body, -1) {
		title := strings.TrimSpace(m[1])
		if !generatedNetworkSections[title] {
			return specErrorf("network.bkn section %q is unknown; network.bkn holds only the exporter's Network Overview and Directory Structure after the description", "## "+title)
		}
	}
	return nil
}

// checkDescriptionV3 refuses a description line that starts with #: it reads as a heading and
// breaks the section structure. The exporter writes such a line as \#.
func checkDescriptionV3(desc string) error {
	for i, line := range strings.Split(desc, "\n") {
		if strings.HasPrefix(line, "#") {
			return specErrorf("description line %d starts with #, which reads as a heading; write it as \\#", i+1)
		}
	}
	return nil
}

func checkSectionsV3(kind string, sections map[string]string, order []string) error {
	rules := sectionRules[kind]
	for _, title := range order {
		if to := renamedSections[kind][title]; to != "" {
			return specErrorf("section %q is not a 3.0 section; write %q", "### "+title, "### "+to)
		}
		if _, ok := rules[title]; !ok {
			return specErrorf("section %q is unknown; the %s sections are %s", "### "+title, kind, sectionList(rules))
		}
	}
	if kind == kindRelationType {
		if err := checkRelationMappingSections(sections, order); err != nil {
			return err
		}
	}
	for _, title := range order {
		if err := checkSectionContent(title, rules[title], sections[title]); err != nil {
			return err
		}
	}
	return nil
}

// checkRelationMappingSections refuses a mapping section the relation's Endpoint Type does not use:
// the 2.x reader skips it.
func checkRelationMappingSections(sections map[string]string, order []string) error {
	rows := parseTable(strings.Split(sections["Endpoint"], "\n"))
	if len(rows) == 0 {
		return nil
	}
	typ := rows[0]["Type"]
	used, ok := relationMappingSections[typ]
	if !ok {
		return nil
	}
	if typ != RELATION_MAPPING_TYPE_INDIRECT {
		for _, title := range []string{"Data Properties", "Property Constraints"} {
			if _, declared := sections[title]; declared {
				return specErrorf("section %q: only indirect relations have relation properties, but Endpoint Type is %s", "### "+title, typ)
			}
		}
	}
	for _, title := range order {
		for mappingType, titles := range relationMappingSections {
			if mappingType != typ && containsString(titles, title) && !containsString(used, title) {
				return specErrorf("section %q belongs to %s relations, but Endpoint Type is %s; write %s",
					"### "+title, mappingType, typ, quoteSections(used))
			}
		}
	}
	return nil
}

func checkSectionContent(title string, rule sectionRule, text string) error {
	where := fmt.Sprintf("section %q", "### "+title)
	switch rule.content {
	case contentTable:
		return checkTableContent(where, rule.table, text)
	case contentYAML:
		return checkYAMLContent(where, title, text)
	case contentKeys:
		return checkKeysContent(where, text)
	case contentLogic:
		return checkLogicContent(text)
	}
	return nil
}

// rawTable is one Markdown table as written: its lines trimmed, with the index of the first line in
// the scanned text.
type rawTable struct {
	lines []string
	start int
}

// scannedContent is what a section holds: its tables and fenced code blocks.
type scannedContent struct {
	tables []rawTable
	blocks []fencedBlock
}

type fencedBlock struct {
	lang string
	body string
}

// scanContent splits text into tables and fenced code blocks. A table is a run of lines that start
// with |; lines inside a fence are never table lines.
func scanContent(text string) scannedContent {
	var out scannedContent
	var fence *fencedBlock
	var fenceBody []string
	var table *rawTable
	closeTable := func() {
		if table != nil {
			out.tables = append(out.tables, *table)
			table = nil
		}
	}
	for i, line := range strings.Split(text, "\n") {
		s := strings.TrimSpace(line)
		if fence != nil {
			if strings.HasPrefix(s, "```") {
				fence.body = strings.Join(fenceBody, "\n")
				out.blocks = append(out.blocks, *fence)
				fence, fenceBody = nil, nil
				continue
			}
			fenceBody = append(fenceBody, line)
			continue
		}
		if strings.HasPrefix(s, "```") {
			closeTable()
			fence = &fencedBlock{lang: strings.TrimSpace(strings.TrimPrefix(s, "```"))}
			continue
		}
		if strings.HasPrefix(s, "|") {
			if table == nil {
				table = &rawTable{start: i}
			}
			table.lines = append(table.lines, s)
			continue
		}
		closeTable()
	}
	closeTable()
	if fence != nil {
		fence.body = strings.Join(fenceBody, "\n")
		out.blocks = append(out.blocks, *fence)
	}
	// Only a yaml block is read; any other fence is text to the reader and is not counted.
	yamlBlocks := out.blocks[:0]
	for _, b := range out.blocks {
		if b.lang == "yaml" {
			yamlBlocks = append(yamlBlocks, b)
		}
	}
	out.blocks = yamlBlocks
	return out
}

func checkTableContent(where string, rule tableRule, text string) error {
	content := scanContent(text)
	if len(content.blocks) > 0 {
		return specErrorf("%s holds a table, not a code block", where)
	}
	// A row cut by an unescaped line break splits into two tables; report the cut, not the split.
	for _, t := range content.tables {
		if err := checkRowsClosed(where, t); err != nil {
			return err
		}
	}
	if len(content.tables) > 1 {
		return specErrorf("%s holds a second table; a section holds one table", where)
	}
	if len(content.tables) == 0 {
		return nil
	}
	return checkTable(where, rule, content.tables[0])
}

func checkRowsClosed(where string, t rawTable) error {
	for i, line := range t.lines {
		if !strings.HasSuffix(line, "|") {
			return specErrorf("%s table line %d does not end with |; a line break inside a cell is written as <br>", where, i+1)
		}
	}
	return nil
}

// checkTable applies the 3.0 table rules: a separator row, closed rows, the listed columns once each,
// no cell beyond the header, and one data row where the section reads only one.
func checkTable(where string, rule tableRule, t rawTable) error {
	if len(t.lines) < 2 || !tableSepRE.MatchString(t.lines[1]) {
		return specErrorf("%s table is missing the separator row under the header", where)
	}
	header := splitRow(t.lines[0])
	if rule.legacyLayout {
		if err := checkLegacyParameterLayout(where, t); err != nil {
			return err
		}
	}
	if err := checkColumns(where, rule, header); err != nil {
		return err
	}
	rows := t.lines[2:]
	for i, line := range rows {
		if n := len(splitRow(line)); n > len(header) {
			return specErrorf("%s row %d has %d cells, more than the %d columns of the header", where, i+1, n, len(header))
		}
	}
	if rule.singleRow && len(rows) > 1 {
		return specErrorf("%s holds %d rows; it reads one row", where, len(rows))
	}
	return nil
}

// checkLegacyParameterLayout refuses the legacy parameter layout Parameter | Source | Binding, which
// put the value source in Source. 3.0 no longer converts it; the message shows the rewrite of the
// first row the conversion would have made.
func checkLegacyParameterLayout(where string, t rawTable) error {
	rows := finishTable(t.lines)
	if len(rows) == 0 {
		return nil
	}
	name := firstNonEmpty(rows[0], "Name", "Parameter")
	suggested, legacy := legacyBindingParameter(rows[0], name)
	if !legacy {
		return nil
	}
	return specErrorf("%s uses the legacy layout Parameter | Source | Binding; write Name | Type | Source | Operation | ValueFrom | Value | Description, with the value source in ValueFrom, e.g. row %q as ValueFrom %q, Value %q",
		where, name, suggested.ValueFrom, suggested.Value)
}

func checkColumns(where string, rule tableRule, header []string) error {
	seen := make(map[string]bool, len(header))
	for _, column := range header {
		if seen[column] {
			return specErrorf("%s column %q appears twice", where, column)
		}
		seen[column] = true
		switch {
		case containsString(rule.columns, column):
		case rule.renamed[column] != "":
			return specErrorf("%s column %q is not a 3.0 column; write %q", where, column, rule.renamed[column])
		case rule.refused[column] != "":
			return specErrorf("%s column %q is not a 3.0 column; write %s", where, column, rule.refused[column])
		default:
			return specErrorf("%s column %q is unknown; the columns are %s", where, column, strings.Join(rule.columns, ", "))
		}
	}
	return nil
}

func checkYAMLContent(where, title, text string) error {
	content := scanContent(text)
	if len(content.tables) > 0 {
		return specErrorf("%s holds a yaml code block, not a table", where)
	}
	if len(content.blocks) > 1 {
		return specErrorf("%s holds a second code block; a section holds one yaml block", where)
	}
	if len(content.blocks) == 0 {
		if strings.TrimSpace(text) != "" {
			return specErrorf("%s holds no yaml code block; write the content in a ```yaml block", where)
		}
		return nil
	}
	block := content.blocks[0]
	var value any
	if err := yaml.Unmarshal([]byte(block.body), &value); err != nil {
		return specErrorf("%s yaml is invalid: %v", where, err)
	}
	switch title {
	case "Trigger Condition", "Source Condition", "Target Condition":
		if m, ok := value.(map[string]any); ok {
			for _, key := range conditionWrapperKeys {
				if _, wrapped := m[key]; wrapped {
					return specErrorf("%s wraps the condition in %q; write the condition itself", where, key+":")
				}
			}
		}
	case "Impact Contracts":
		m, ok := value.(map[string]any)
		if _, keyed := m["impact_contracts"]; value != nil && (!ok || len(m) != 1 || !keyed) {
			return specErrorf("%s must be a list under the single key impact_contracts:", where)
		}
	}
	return nil
}

func checkKeysContent(where, text string) error {
	if content := scanContent(text); len(content.tables) > 0 || len(content.blocks) > 0 {
		return specErrorf("%s holds key lines, not a table or code block", where)
	}
	for i, line := range strings.Split(text, "\n") {
		s := strings.TrimSpace(line)
		if s == "" {
			continue
		}
		isKeyLine := false
		for _, p := range keyLinePrefixes {
			isKeyLine = isKeyLine || strings.HasPrefix(s, p)
		}
		if !isKeyLine {
			return specErrorf("%s line %d is not a key line; the lines are %s", where, i+1, strings.Join(keyLinePrefixes, ", "))
		}
	}
	return nil
}

// checkLogicContent applies the 3.0 rules to ### Logic Properties: one #### item per property, each
// holding tables under the four bold labels.
func checkLogicContent(text string) error {
	const where = `section "### Logic Properties"`
	matches := subSectionRE.FindAllStringSubmatchIndex(text, -1)
	head := text
	if len(matches) > 0 {
		head = text[:matches[0][0]]
	}
	if content := scanContent(head); len(content.tables) > 0 || len(content.blocks) > 0 {
		return specErrorf("%s uses the flat table; write one #### item per logic property with **Meta**, **Source**, **Parameters** and **Analysis Dimensions** tables", where)
	}
	for i, m := range matches {
		name := strings.TrimSpace(text[m[2]:m[3]])
		end := len(text)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		if err := checkLogicItem(fmt.Sprintf("logic property %q", name), text[m[1]:end]); err != nil {
			return err
		}
	}
	return nil
}

func checkLogicItem(where, text string) error {
	parts := map[string][]string{}
	var order []string
	label := ""
	for _, line := range strings.Split(text, "\n") {
		s := strings.TrimSpace(line)
		if m := boldLabelRE.FindStringSubmatch(s); len(m) == 2 {
			label = m[1]
			if _, ok := logicLabelRules[label]; !ok {
				return specErrorf("%s label %q is unknown; the labels are **Meta**, **Source**, **Parameters** and **Analysis Dimensions**", where, s)
			}
			parts[label] = nil
			order = append(order, label)
			continue
		}
		if label == "" {
			continue
		}
		parts[label] = append(parts[label], line)
	}
	for _, l := range order {
		if err := checkTableContent(fmt.Sprintf("%s **%s**", where, l), logicLabelRules[l], strings.Join(parts[l], "\n")); err != nil {
			return err
		}
	}
	return nil
}

// rawFrontmatter is frontmatter read as YAML nodes, so 3.0 reads a string field as written: 001
// stays 001 and 1.0 stays 1.0 instead of becoming a number on the way through.
type rawFrontmatter struct {
	keys   []string
	values map[string]*yaml.Node
}

func parseRawFrontmatter(text string) (rawFrontmatter, error) {
	fm := rawFrontmatter{values: map[string]*yaml.Node{}}
	fmStr, _ := splitFrontmatter(text)
	if fmStr == "" {
		return fm, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(fmStr), &doc); err != nil {
		return fm, err
	}
	if len(doc.Content) == 0 {
		return fm, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return fm, specErrorf("frontmatter must be a YAML mapping")
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		key := root.Content[i].Value
		fm.keys = append(fm.keys, key)
		fm.values[key] = root.Content[i+1]
	}
	return fm, nil
}

// str is the text of a scalar field as written; empty when the field is absent, null or not a
// scalar.
func (fm rawFrontmatter) str(key string) string {
	node, ok := fm.values[key]
	if !ok || node.Kind != yaml.ScalarNode || node.Tag == "!!null" {
		return ""
	}
	return node.Value
}

// list is a sequence field's items as written.
func (fm rawFrontmatter) list(key string) []string {
	node, ok := fm.values[key]
	if !ok || node.Kind != yaml.SequenceNode {
		return nil
	}
	items := make([]string, 0, len(node.Content))
	for _, item := range node.Content {
		if item.Tag != "!!null" {
			items = append(items, item.Value)
		}
	}
	return items
}

// unescapeDescriptionV3 reverses the exporter's escape of description lines that start with #: one
// leading backslash is removed from a line that starts with backslashes followed by #.
func unescapeDescriptionV3(desc string) string {
	return mapLines(desc, func(line string) string {
		if escapedHashLineRE.MatchString(line) {
			return strings.TrimPrefix(line, `\`)
		}
		return line
	})
}

// escapeDescriptionV3 makes every description line that would read as a heading literal: a line
// that starts with zero or more backslashes followed by # gets one more backslash.
func escapeDescriptionV3(desc string) string {
	return mapLines(desc, func(line string) string {
		if hashLineRE.MatchString(line) {
			return `\` + line
		}
		return line
	})
}

var (
	hashLineRE        = regexp.MustCompile(`^\\*#`)
	escapedHashLineRE = regexp.MustCompile(`^\\+#`)
)

func mapLines(text string, f func(string) string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = f(line)
	}
	return strings.Join(lines, "\n")
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func sectionList(rules map[string]sectionRule) string {
	titles := make([]string, 0, len(rules))
	for title := range rules {
		titles = append(titles, title)
	}
	sort.Strings(titles)
	return quoteSections(titles)
}

func quoteSections(titles []string) string {
	quoted := make([]string, len(titles))
	for i, title := range titles {
		quoted[i] = fmt.Sprintf("%q", "### "+title)
	}
	return strings.Join(quoted, ", ")
}
