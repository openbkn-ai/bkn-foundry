// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common/maskrule"

	"gopkg.in/yaml.v3"
)

var sectionRE = regexp.MustCompile(`(?m)^###\s+(.+)$`)
var subSectionRE = regexp.MustCompile(`(?m)^####\s+(.+)$`)

var knownObjectTypeSections = map[string]bool{
	"Data Source":      true,
	"Data Properties":  true,
	"Logic Properties": true,
	"Keys":             true,
}

var knownRelationTypeSections = map[string]bool{
	"Endpoint":         true,
	"Mapping Rules":    true,
	"Backing Resource": true,
	"Source Mapping":   true,
	"Target Mapping":   true,
	"Source Condition": true,
	"Target Condition": true,
}

var knownActionTypeSections = map[string]bool{
	"Bound Object":      true,
	"Affect Object":     true,
	"Impact Contracts":  true,
	"Trigger Condition": true,
	"Action Source":     true,
	"Parameter Binding": true,
	"Schedule":          true,
}

var knownRiskTypeSections = map[string]bool{}

var knownConceptGroupSections = map[string]bool{
	"Object Types": true,
}

var h1HeadingRE = regexp.MustCompile(`(?m)^#\s+(.+)$`)
var h2HeadingRE = regexp.MustCompile(`(?m)^##\s+(.+)$`)
var tableSepRE = regexp.MustCompile(`^\|?[\s:*-]+(\|[\s:*-]+)*\|?$`)
var yamlBlockRE = regexp.MustCompile("(?s)```yaml\\s*\\n(.+?)```")

// extractBodyDescription extracts the description text from the body.
// For network files (with # heading): extracts between # and ##
// For other files (with ## heading): extracts between ## and ###
func extractBodyDescription(text string) string {
	_, body := splitFrontmatter(text)

	// Check if there's a # heading (H1) - network file format
	h1Loc := h1HeadingRE.FindStringIndex(body)
	if h1Loc != nil {
		// Start after the # heading line
		rest := body[h1Loc[1]:]
		// Find the first ## section (H2) - this marks the end of description
		secLoc := h2HeadingRE.FindStringIndex(rest)
		if secLoc == nil {
			return strings.TrimSpace(rest)
		}
		return strings.TrimSpace(rest[:secLoc[0]])
	}

	// Check if there's a ## heading (H2) - object_type/relation_type etc. format
	h2Loc := h2HeadingRE.FindStringIndex(body)
	if h2Loc != nil {
		// Start after the ## heading line
		rest := body[h2Loc[1]:]
		// Find the first ### section - this marks the end of description
		secLoc := sectionRE.FindStringIndex(rest)
		if secLoc == nil {
			return strings.TrimSpace(rest)
		}
		return strings.TrimSpace(rest[:secLoc[0]])
	}

	return ""
}

// ExtractSummary derives a one-sentence summary from a description:
// the text up to the first "。", ". " (period+space), or "\n", whichever comes first.
func ExtractSummary(desc string) string {
	if desc == "" {
		return ""
	}
	end := len(desc)
	for _, sep := range []string{"。", ". ", "\n"} {
		if i := strings.Index(desc, sep); i >= 0 && i < end {
			end = i + len(sep)
		}
	}
	return strings.TrimSpace(desc[:end])
}

func splitFrontmatter(text string) (fm string, body string) {
	text = strings.TrimPrefix(text, "\ufeff")
	if !strings.HasPrefix(text, "---") {
		return "", text
	}
	end := strings.Index(text[3:], "\n---")
	if end == -1 {
		return "", text
	}
	end += 3
	idx := strings.Index(text[end+3:], "\n")
	if idx == -1 {
		return strings.TrimSpace(text[3:end]), ""
	}
	fm = strings.TrimSpace(text[3:end])
	body = text[end+4+idx:]
	return fm, body
}

// splitRow splits one table row into cells. A pipe escaped as \| stays inside its cell, and the
// <br> the serializer writes for a line break becomes a line break again, so a value survives
// the round trip through the file unchanged.
func splitRow(row string) []string {
	row = strings.TrimSpace(row)
	row = strings.TrimPrefix(row, "|")
	row = strings.TrimSuffix(row, "|")
	var parts []string
	var cell strings.Builder
	for i := 0; i < len(row); i++ {
		switch {
		case row[i] == '\\' && i+1 < len(row) && row[i+1] == '|':
			cell.WriteByte('|')
			i++
		case row[i] == '|':
			parts = append(parts, cell.String())
			cell.Reset()
		default:
			cell.WriteByte(row[i])
		}
	}
	parts = append(parts, cell.String())
	for i := range parts {
		cell := strings.ReplaceAll(parts[i], "<br>", "\n")
		parts[i] = strings.TrimSpace(strings.ReplaceAll(cell, "&lt;br&gt;", "<br>"))
	}
	return parts
}

func parseTable(lines []string) []map[string]string {
	var tableLines []string
	for _, line := range lines {
		s := strings.TrimSpace(line)
		switch {
		case len(tableLines) > 0 && !strings.HasSuffix(tableLines[len(tableLines)-1], "|") && !strings.HasPrefix(s, "#"):
			// The previous row is still open: an exporter that did not escape a line break inside
			// a cell wrote the rest of the row on this line. Joining it back is what keeps files
			// exported before the escape existed importable in full instead of silently losing
			// every row after the break.
			tableLines[len(tableLines)-1] += "\n" + s
		case strings.HasPrefix(s, "|"):
			tableLines = append(tableLines, s)
		case len(tableLines) > 0:
			return finishTable(tableLines)
		}
	}
	return finishTable(tableLines)
}

func finishTable(tableLines []string) []map[string]string {
	if len(tableLines) < 2 {
		return nil
	}
	headers := splitRow(tableLines[0])
	sepLine := strings.TrimSpace(tableLines[1])
	dataStart := 2
	if !tableSepRE.MatchString(sepLine) {
		dataStart = 1
	}
	var rows []map[string]string
	for _, line := range tableLines[dataStart:] {
		cells := splitRow(line)
		row := make(map[string]string)
		for i, h := range headers {
			if i < len(cells) {
				row[h] = cells[i]
			} else {
				row[h] = ""
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// extractSectionsWithDesc splits a BKN entity file into the pre-section description
// and all ### sections (map + document order). One pass replaces both
// extractBodyDescription and extractSections for ### level.
func extractSectionsWithDesc(text string) (desc string, sections map[string]string, order []string) {
	_, body := splitFrontmatter(text)

	h2Loc := h2HeadingRE.FindStringIndex(body)
	if h2Loc == nil {
		return "", make(map[string]string), nil
	}
	rest := body[h2Loc[1]:]

	matches := sectionRE.FindAllStringSubmatchIndex(rest, -1)
	sections = make(map[string]string, len(matches))
	if len(matches) == 0 {
		return strings.TrimSpace(rest), sections, nil
	}

	desc = strings.TrimSpace(rest[:matches[0][0]])
	for i, m := range matches {
		title := strings.TrimSpace(rest[m[2]:m[3]])
		start := m[1]
		end := len(rest)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		sections[title] = strings.TrimSpace(rest[start:end])
		order = append(order, title)
	}
	return
}

// buildDescription joins the pre-section description text with any unknown sections
// (sections whose titles are not in knownSections), preserving document order.
func buildDescription(desc string, sections map[string]string, order []string, knownSections map[string]bool) string {
	var parts []string
	if desc != "" {
		parts = append(parts, desc)
	}
	for _, title := range order {
		if !knownSections[title] {
			parts = append(parts, "### "+title+"\n\n"+sections[title])
		}
	}
	return strings.Join(parts, "\n\n")
}

func extractSections(body string, level string) map[string]string {
	var re *regexp.Regexp
	if level == "###" {
		re = sectionRE
	} else {
		re = subSectionRE
	}
	matches := re.FindAllStringSubmatchIndex(body, -1)
	sections := make(map[string]string)
	for i, m := range matches {
		title := strings.TrimSpace(body[m[2]:m[3]])
		start := m[1]
		end := len(body)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		sections[title] = strings.TrimSpace(body[start:end])
	}
	return sections
}

func parseDataSource(sectionText string) *ResourceInfo {
	rows := parseTable(strings.Split(sectionText, "\n"))
	if len(rows) == 0 {
		return nil
	}
	r := rows[0]
	return &ResourceInfo{
		Type: r["Type"],
		ID:   r["ID"],
		Name: r["Name"],
	}
}

// parseDataProperties reads the Data Properties table. Under 3.0 it also reads the Semantic,
// Required and Aliases columns; explicitFalse names the properties whose Required is written false.
func parseDataProperties(sectionText string, opts parseOptions) (props []*DataProperty, explicitFalse []string, err error) {
	rows := parseTable(strings.Split(sectionText, "\n"))
	for _, row := range rows {
		property := &DataProperty{
			Name:        row["Name"],
			DisplayName: row["Display Name"],
			Type:        row["Type"],
			Description: row["Description"],
			MappedField: row["Mapped Field"],
		}
		if strings.TrimSpace(property.Name) == "" {
			// A row without a name is a row the table reader could not rebuild. Refusing it
			// here is what makes a broken file visible; dropping the row would import an
			// object type that is quietly missing a property.
			return nil, nil, fmt.Errorf("data property row %d is malformed: empty Name", len(props)+1)
		}
		if rawRule := strings.TrimSpace(row["Mask Rule"]); rawRule != "" {
			var rule maskrule.Rule
			if err := json.Unmarshal([]byte(rawRule), &rule); err != nil {
				return nil, nil, fmt.Errorf("data property %q has invalid Mask Rule JSON: %w", property.Name, err)
			}
			property.MaskRule = &rule
		}
		if opts.v3() {
			property.Semantic = strings.TrimSpace(row["Semantic"])
			property.Aliases = parseAliasesCell(row["Aliases"])
			required, isFalse, err := parseRequired(property.Name, row["Required"])
			if err != nil {
				return nil, nil, err
			}
			property.Required = required
			if isFalse {
				explicitFalse = append(explicitFalse, property.Name)
			}
		}
		props = append(props, property)
	}
	return props, explicitFalse, nil
}

func parseLogicProperties(sectionText string) []*LogicProperty {
	// Sub-section format: #### name + inline metadata + parameter table
	subSections := extractSections(sectionText, "####")
	if len(subSections) > 0 {
		// Use ordered extraction to preserve sub-section order
		matches := subSectionRE.FindAllStringSubmatchIndex(sectionText, -1)
		var props []*LogicProperty
		for i, m := range matches {
			name := strings.TrimSpace(sectionText[m[2]:m[3]])
			start := m[1]
			end := len(sectionText)
			if i+1 < len(matches) {
				end = matches[i+1][0]
			}
			content := strings.TrimSpace(sectionText[start:end])
			props = append(props, parseLogicPropertySubSection(name, content))
		}
		return props
	}

	// Flat table format
	rows := parseTable(strings.Split(sectionText, "\n"))
	var props []*LogicProperty
	for _, row := range rows {
		props = append(props, &LogicProperty{
			Name:        row["Name"],
			DisplayName: row["Display Name"],
			Type:        row["Type"],
			Description: row["Description"],
		})
	}
	return props
}

// boldLabelRE matches a bold label anchor like **Meta** at the start of a trimmed line.
var boldLabelRE = regexp.MustCompile(`^\*\*(.+?)\*\*$`)

func parseLogicPropertySubSection(name, content string) *LogicProperty {
	prop := &LogicProperty{Name: name}
	lines := strings.Split(content, "\n")
	var currentLabel string
	var tableLines []string

	flush := func() {
		if currentLabel == "" || len(tableLines) == 0 {
			return
		}
		rows := parseTable(tableLines)
		switch currentLabel {
		case "Meta":
			if len(rows) > 0 {
				prop.DisplayName = rows[0]["Display Name"]
				prop.Type = rows[0]["Type"]
				prop.Description = rows[0]["Description"]
			}
		case "Source":
			if len(rows) > 0 {
				prop.DataSource = &ResourceInfo{
					Type:       rows[0]["Source Type"],
					ID:         rows[0]["Source ID"],
					Name:       rows[0]["Source Name"],
					BoxID:      firstNonEmpty(rows[0], "BoxID", "Box ID", "Toolbox ID"),
					ToolID:     firstNonEmpty(rows[0], "ToolID", "Tool ID"),
					ResultPath: firstNonEmpty(rows[0], "ResultPath", "Result Path"),
				}
			}
		case "Parameters":
			for _, row := range rows {
				v := any(row["Value"])
				if row["Value"] == "" {
					v = nil
				}
				prop.Parameters = append(prop.Parameters, Parameter{
					Name:        row["Name"],
					Type:        row["Type"],
					Source:      row["Source"],
					Operation:   row["Operation"],
					ValueFrom:   row["ValueFrom"],
					Value:       v,
					Description: row["Description"],
				})
			}
		case "Analysis Dimensions":
			for _, row := range rows {
				prop.AnalysisDims = append(prop.AnalysisDims, Field{
					Name:        row["Name"],
					DisplayName: row["Display Name"],
					Type:        row["Type"],
					Description: row["Description"],
				})
			}
		}
		tableLines = nil
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if m := boldLabelRE.FindStringSubmatch(trimmed); len(m) == 2 {
			flush()
			currentLabel = m[1]
			continue
		}
		// Every line between two labels goes to the table reader as-is. Filtering to lines that
		// start with a pipe here would drop the tail of a row an old exporter split at a line
		// break, and parseTable would then join the *next* row onto the open one — losing a
		// parameter instead of restoring one. parseTable already skips what is not a table.
		tableLines = append(tableLines, trimmed)
	}
	flush()
	return prop
}

func parseKeys(sectionText string) (pks []string, dk string, ik string) {
	for _, line := range strings.Split(sectionText, "\n") {
		trimmed := strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(trimmed, "Primary Keys:"); ok {
			val := strings.TrimSpace(after)
			if val != "" {
				pks = strings.Split(val, ",")
				for i := range pks {
					pks[i] = strings.TrimSpace(pks[i])
				}
			}
		} else if after, ok := strings.CutPrefix(trimmed, "Display Key:"); ok {
			dk = strings.TrimSpace(after)
		} else if after, ok := strings.CutPrefix(trimmed, "Incremental Key:"); ok {
			ik = strings.TrimSpace(after)
		}
	}
	return pks, dk, ik
}

// ParseFrontmatter parses the YAML frontmatter of a .bkn file.
func ParseFrontmatter(text string) (map[string]any, error) {
	fmStr, _ := splitFrontmatter(text)
	if fmStr == "" {
		return map[string]any{}, nil
	}
	var data map[string]any
	if err := yaml.Unmarshal([]byte(fmStr), &data); err != nil {
		return nil, err
	}
	if data == nil {
		data = make(map[string]any)
	}

	return data, nil
}

func strVal(m map[string]any, key string) string {
	if v, ok := m[key]; ok && v != nil {
		return fmt.Sprint(v)
	}
	return ""
}

// strSliceVal safely extracts a string slice from a map value.
// YAML unmarshals arrays as []interface{}, so we need to convert each element.
func strSliceVal(m map[string]any, key string) []string {
	if v, ok := m[key]; ok && v != nil {
		switch val := v.(type) {
		case []string:
			return val
		case []interface{}:
			result := make([]string, 0, len(val))
			for _, item := range val {
				if item != nil {
					result = append(result, fmt.Sprint(item))
				}
			}
			return result
		case string:
			// Handle single string as single-element slice
			return []string{val}
		}
	}
	return nil
}

// ParseNetworkFile parses a network.bkn file (type: network).
// Network files contain only frontmatter, no body definitions.
func ParseNetworkFile(text string, sourcePath string) (*BknNetwork, error) {
	fmData, err := ParseFrontmatter(text)
	if err != nil {
		return nil, err
	}

	// Validate required fields
	if strVal(fmData, "type") == "" {
		return nil, fmt.Errorf("missing required field 'type' in network.bkn frontmatter")
	}
	if strVal(fmData, "id") == "" {
		return nil, fmt.Errorf("missing required field 'id' in network.bkn frontmatter")
	}

	spec, err := normalizeBknSpec(fmData["bkn_spec"])
	if err != nil {
		return nil, err
	}

	network := &BknNetwork{
		BknNetworkFrontmatter: BknNetworkFrontmatter{
			Type:         strVal(fmData, "type"),
			ID:           strVal(fmData, "id"),
			Name:         strVal(fmData, "name"),
			Tags:         strSliceVal(fmData, "tags"),
			Version:      strVal(fmData, "version"),
			Branch:       strVal(fmData, "branch"),
			BknSpec:      spec,
			Capabilities: parseCapabilities(fmData),
		},
		Description: extractBodyDescription(text),
		RawContent:  text,
	}
	if spec == BknSpecV3 {
		fm, err := parseRawFrontmatter(text)
		if err != nil {
			return nil, err
		}
		if err := checkNetworkV3(text, fm); err != nil {
			return nil, fmt.Errorf("network.bkn: %w", err)
		}
		network.ID, network.Name, network.Tags = fm.str("id"), fm.str("name"), fm.list("tags")
		network.Version, network.Branch = fm.str("version"), fm.str("branch")
		network.Description = unescapeDescriptionV3(network.Description)
	}
	network.Summary = ExtractSummary(network.Description)

	return network, nil
}

// ParseObjectTypeFile parses an object_type definition file by the 2.x rules.
func ParseObjectTypeFile(text string, sourcePath string) (*BknObjectType, error) {
	return parseObjectTypeFile(text, sourcePath, parseOptions{})
}

func parseObjectTypeFile(text string, sourcePath string, opts parseOptions) (*BknObjectType, error) {
	fmData, err := ParseFrontmatter(text)
	if err != nil {
		return nil, err
	}

	header, err := readDefinitionHeader(kindObjectType, text, fmData, opts)
	if err != nil {
		return nil, err
	}
	desc, sections, order := extractSectionsWithDesc(text)

	obj := &BknObjectType{
		BknObjectTypeFrontmatter: BknObjectTypeFrontmatter{
			Type:    "object_type",
			ID:      header.id,
			Name:    header.name,
			Tags:    header.tags,
			Aliases: header.aliases,
		},
		Description: header.description(buildDescription(desc, sections, order, opts.knownObjectSections())),
		RawContent:  text,
	}
	obj.Summary = ExtractSummary(obj.Description)

	_, obj.HasDataPropertiesSection = sections["Data Properties"]
	_, obj.HasKeysSection = sections["Keys"]

	if s, ok := sections["Data Source"]; ok {
		obj.DataSource = parseDataSource(s)
	}
	var explicitFalse []string
	if s, ok := sections["Data Properties"]; ok {
		obj.DataProperties, explicitFalse, err = parseDataProperties(s, opts)
		if err != nil {
			return nil, err
		}
	}
	if s, ok := sections["Logic Properties"]; ok {
		obj.LogicProperties = parseLogicProperties(s)
		if opts.v3() {
			for _, lp := range obj.LogicProperties {
				for _, p := range lp.Parameters {
					if err := checkParameterValueSource(fmt.Sprintf("logic property %q", lp.Name), p); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	if s, ok := sections["Keys"]; ok {
		pks, dk, ik := parseKeys(s)
		obj.PrimaryKeys = pks
		obj.DisplayKey = dk
		obj.IncrementalKey = ik
	}
	if opts.v3() {
		if err := parseObjectTypeDeclarationsV3(obj, sections, explicitFalse); err != nil {
			return nil, err
		}
	}

	return obj, nil
}

// parseObjectTypeDeclarationsV3 reads ### Property Constraints and checks the property declarations
// of a 3.0 object type.
func parseObjectTypeDeclarationsV3(obj *BknObjectType, sections map[string]string, explicitFalse []string) error {
	for _, name := range explicitFalse {
		if containsString(obj.PrimaryKeys, name) {
			return specErrorf("data property %q: a primary key is required by definition; leave Required empty instead of writing false", name)
		}
	}
	for _, p := range obj.DataProperties {
		if containsString(obj.PrimaryKeys, p.Name) {
			p.Required = true
		}
	}
	if s, ok := sections["Property Constraints"]; ok {
		if err := parsePropertyConstraintsV3(s, obj.DataProperties); err != nil {
			return err
		}
	}
	return ValidateObjectTypeDeclarations(obj)
}

// ParseRelationTypeFile parses a relation_type definition file by the 2.x rules.
func ParseRelationTypeFile(text string, sourcePath string) (*BknRelationType, error) {
	return parseRelationTypeFile(text, sourcePath, parseOptions{})
}

func parseRelationTypeFile(text string, sourcePath string, opts parseOptions) (*BknRelationType, error) {
	fmData, err := ParseFrontmatter(text)
	if err != nil {
		return nil, err
	}
	header, err := readDefinitionHeader(kindRelationType, text, fmData, opts)
	if err != nil {
		return nil, err
	}

	desc, sections, order := extractSectionsWithDesc(text)

	rel := &BknRelationType{
		BknRelationTypeFrontmatter: BknRelationTypeFrontmatter{
			Type: "relation_type",
			ID:   header.id,
			Name: header.name,
			Tags: header.tags,
		},
		Description: header.description(buildDescription(desc, sections, order, knownRelationTypeSections)),
		RawContent:  text,
	}
	rel.Summary = ExtractSummary(rel.Description)

	if s, ok := sections["Endpoint"]; ok {
		rows := parseTable(strings.Split(s, "\n"))
		if len(rows) > 0 {
			row := rows[0]
			rel.Endpoint = Endpoint{
				Source: row["Source"],
				Target: row["Target"],
				Type:   row["Type"],
			}
		}
	}

	switch rel.Endpoint.Type {
	case RELATION_MAPPING_TYPE_DIRECT:
		if s, ok := sections["Mapping Rules"]; ok {
			rows := parseTable(strings.Split(s, "\n"))
			var rules []MappingRule
			for _, row := range rows {
				sp, tp := row["Source Property"], row["Target Property"]
				if sp != "" || tp != "" {
					rules = append(rules, MappingRule{SourceProperty: sp, TargetProperty: tp})
				}
			}
			rel.MappingRules = DirectMappingRule(rules)
		}
	case RELATION_MAPPING_TYPE_INDIRECT:
		indirect := &InDirectMappingRule{}
		if s, ok := sections["Backing Resource"]; ok {
			rows := parseTable(strings.Split(s, "\n"))
			if len(rows) > 0 {
				indirect.BackingDataSource = &ResourceInfo{
					Type: rows[0]["Type"],
					ID:   rows[0]["ID"],
				}
			}
		}
		if s, ok := sections["Source Mapping"]; ok {
			rows := parseTable(strings.Split(s, "\n"))
			for _, row := range rows {
				sp, rp := row["Source Property"], row["Resource Property"]
				if sp != "" || rp != "" {
					indirect.SourceMappingRules = append(indirect.SourceMappingRules, MappingRule{SourceProperty: sp, TargetProperty: rp})
				}
			}
		}
		if s, ok := sections["Target Mapping"]; ok {
			rows := parseTable(strings.Split(s, "\n"))
			for _, row := range rows {
				rp, tp := row["Resource Property"], row["Target Property"]
				if rp != "" || tp != "" {
					indirect.TargetMappingRules = append(indirect.TargetMappingRules, MappingRule{SourceProperty: rp, TargetProperty: tp})
				}
			}
		}
		rel.MappingRules = indirect
	case RELATION_MAPPING_TYPE_FILTERED_CROSS_JOIN:
		mapping := &FilteredCrossJoinMapping{}
		if s, ok := sections["Source Condition"]; ok {
			mapping.SourceCondition = parseCondition(s)
		}
		if s, ok := sections["Target Condition"]; ok {
			mapping.TargetCondition = parseCondition(s)
		}
		rel.MappingRules = mapping
	}

	return rel, nil
}

// ParseActionTypeFile parses an action_type definition file by the 2.x rules.
func ParseActionTypeFile(text string, sourcePath string) (*BknActionType, error) {
	return parseActionTypeFile(text, sourcePath, parseOptions{})
}

func parseActionTypeFile(text string, sourcePath string, opts parseOptions) (*BknActionType, error) {
	fmData, err := ParseFrontmatter(text)
	if err != nil {
		return nil, err
	}

	header, err := readDefinitionHeader(kindActionType, text, fmData, opts)
	if err != nil {
		return nil, err
	}
	desc, sections, order := extractSectionsWithDesc(text)

	act := &BknActionType{
		BknActionTypeFrontmatter: BknActionTypeFrontmatter{
			Type:         "action_type",
			ID:           header.id,
			Name:         header.name,
			Tags:         header.tags,
			ActionType:   strVal(fmData, "action_type"),
			ActionIntent: strVal(fmData, "action_intent"),
		},
		Description: header.description(buildDescription(desc, sections, order, opts.knownActionSections())),
		RawContent:  text,
	}
	if opts.v3() {
		act.ActionIntent = header.raw.str("action_intent")
		act.Aliases = header.aliases
		if err := ValidateActionTypeDeclarations(act); err != nil {
			return nil, err
		}
	}
	act.Summary = ExtractSummary(act.Description)

	if s, ok := sections["Bound Object"]; ok {
		bo, at := parseBoundObject(s)
		act.BoundObject = bo
		if act.ActionType == "" && at != "" {
			act.ActionType = at
		}
	}
	if act.ActionType == "" && act.ActionIntent != "" {
		act.ActionType = act.ActionIntent
	}
	if act.ActionIntent == "" && act.ActionType != "" {
		act.ActionIntent = act.ActionType
	}
	if s, ok := sections["Affect Object"]; ok {
		act.AffectObject = parseAffectObject(s)
	}
	if s, ok := sections["Impact Contracts"]; ok {
		act.ImpactContracts = parseImpactContracts(s)
	}
	if s, ok := sections["Trigger Condition"]; ok {
		act.TriggerCondition = parseActionCondition(s)
	}
	if s, ok := sections["Action Source"]; ok {
		act.ActionSource = parseActionSource(s)
	} else if s, ok := sections["Tool Configuration"]; ok {
		act.ActionSource = parseActionSource(s)
	}
	if s, ok := sections["Parameter Binding"]; ok {
		act.Parameters, err = parseParameterBinding(s, opts)
		if err != nil {
			return nil, err
		}
	}
	if s, ok := sections["Schedule"]; ok {
		act.Schedule = parseSchedule(s)
	}

	return act, nil
}

// parseBoundObject parses the bound object section.
// Returns the bound object ID and, if present, the action_type from the table's
// "Action Type" column (used as fallback when frontmatter action_type is empty).
func parseBoundObject(sectionText string) (boundObject string, actionType string) {
	rows := parseTable(strings.Split(sectionText, "\n"))
	if len(rows) == 0 {
		return "", ""
	}
	r := rows[0]
	return r["Bound Object"], r["Action Type"]
}

// parseAffectObject parses the affect object section.
func parseAffectObject(sectionText string) (affectObject *ActionAffect) {
	rows := parseTable(strings.Split(sectionText, "\n"))
	if len(rows) == 0 {
		return nil
	}
	r := rows[0]
	affectObject = &ActionAffect{
		ObjectType:  r["Affect Object"],
		Description: r["Affect Description"],
	}
	return affectObject
}

// parseImpactContracts reads the ### Impact Contracts YAML block (impact_contracts: [...]).
func parseImpactContracts(sectionText string) []*ImpactContractItem {
	matches := yamlBlockRE.FindStringSubmatch(sectionText)
	if len(matches) < 2 {
		return nil
	}
	yamlContent := strings.TrimSpace(matches[1])
	var wrapper struct {
		ImpactContracts []*ImpactContractItem `yaml:"impact_contracts"`
	}
	if err := yaml.Unmarshal([]byte(yamlContent), &wrapper); err == nil && len(wrapper.ImpactContracts) > 0 {
		return wrapper.ImpactContracts
	}
	var list []*ImpactContractItem
	if err := yaml.Unmarshal([]byte(yamlContent), &list); err == nil && len(list) > 0 {
		return list
	}
	return nil
}

// parseActionCondition parses the trigger condition from YAML code block.
// Handles both direct CondCfg and wrapped `condition:` / `trigger_condition:` keys.
func parseCondition(sectionText string) *CondCfg {
	matches := yamlBlockRE.FindStringSubmatch(sectionText)
	if len(matches) < 2 {
		return nil
	}

	yamlContent := matches[1]

	// Try wrapped format first: { condition: {...} } or { trigger_condition: {...} }
	var wrapper map[string]*CondCfg
	if err := yaml.Unmarshal([]byte(yamlContent), &wrapper); err == nil {
		if c, ok := wrapper["condition"]; ok && c != nil {
			return c
		}
		if c, ok := wrapper["trigger_condition"]; ok && c != nil {
			return c
		}
	}

	// Fall back to direct CondCfg
	var cond CondCfg
	if err := yaml.Unmarshal([]byte(yamlContent), &cond); err != nil {
		return nil
	}
	if cond.Operation == "" && cond.Field == "" && len(cond.SubConds) == 0 {
		return nil
	}
	return &cond
}

// parseActionCondition parses the trigger condition from YAML code block.
// Handles both direct CondCfg and wrapped `condition:` / `trigger_condition:` keys.
func parseActionCondition(sectionText string) *ActionCondCfg {
	matches := yamlBlockRE.FindStringSubmatch(sectionText)
	if len(matches) < 2 {
		return nil
	}

	yamlContent := matches[1]

	// Try wrapped format first: { condition: {...} } or { trigger_condition: {...} }
	var wrapper map[string]*ActionCondCfg
	if err := yaml.Unmarshal([]byte(yamlContent), &wrapper); err == nil {
		if c, ok := wrapper["condition"]; ok && c != nil {
			return c
		}
		if c, ok := wrapper["trigger_condition"]; ok && c != nil {
			return c
		}
	}

	// Fall back to direct CondCfg
	var cond ActionCondCfg
	if err := yaml.Unmarshal([]byte(yamlContent), &cond); err != nil {
		return nil
	}
	if cond.Operation == "" && cond.Field == "" && len(cond.SubConds) == 0 {
		return nil
	}
	return &cond
}

// parseParameterBinding parses the parameter binding table.
// Current column headers: Name | Type | Source | Operation | ValueFrom | Value | Description.
// Legacy aliases "Parameter" (for Name) and "Binding"/"Value From" (for ValueFrom) are
// also accepted for backwards compatibility with older BKN files.
//
// Under 3.0 the table has a single layout (the legacy one is refused before parsing, see strict.go)
// and a row whose value source would make execution drop the parameter is refused.
func parseParameterBinding(sectionText string, opts parseOptions) ([]Parameter, error) {
	rows := parseTable(strings.Split(sectionText, "\n"))
	var params []Parameter
	for _, row := range rows {
		name := firstNonEmpty(row, "Name", "Parameter")
		valueFrom := firstNonEmpty(row, "ValueFrom", "Binding", "Value From")
		param := Parameter{
			Name:        name,
			Type:        row["Type"],
			Source:      row["Source"],
			Operation:   row["Operation"],
			ValueFrom:   valueFrom,
			Value:       row["Value"],
			Description: row["Description"],
		}
		if opts.v3() {
			if err := checkParameterValueSource("action", param); err != nil {
				return nil, err
			}
		}
		params = append(params, param)
	}
	return params, nil
}

// parseActionSource parses the action source table.
// Handles multiple column naming conventions found in BKN files.
func parseActionSource(sectionText string) *ActionSource {
	rows := parseTable(strings.Split(sectionText, "\n"))
	if len(rows) == 0 {
		return nil
	}
	r := rows[0]

	actSrc := &ActionSource{
		Type: r["Type"],
	}
	switch strings.ToLower(strings.TrimSpace(actSrc.Type)) {
	case "tool":
		actSrc.BoxID = firstNonEmpty(r, "BoxID", "Box ID", "Toolbox ID")
		actSrc.ToolID = firstNonEmpty(r, "ToolID", "Tool ID")
	case "mcp":
		actSrc.McpID = firstNonEmpty(r, "McpID", "MCP ID", "Mcp ID")
		actSrc.ToolName = firstNonEmpty(r, "ToolName", "Tool Name")
	}

	return actSrc
}

// parseSchedule parses the schedule table.
func parseSchedule(sectionText string) *Schedule {
	rows := parseTable(strings.Split(sectionText, "\n"))
	if len(rows) == 0 {
		return nil
	}
	r := rows[0]
	return &Schedule{
		Type:       r["Type"],
		Expression: r["Expression"],
	}
}

// ParseRiskTypeFile parses a risk_type definition file by the 2.x rules.
func ParseRiskTypeFile(text string, sourcePath string) (*BknRiskType, error) {
	return parseRiskTypeFile(text, sourcePath, parseOptions{})
}

func parseRiskTypeFile(text string, sourcePath string, opts parseOptions) (*BknRiskType, error) {
	fmData, err := ParseFrontmatter(text)
	if err != nil {
		return nil, err
	}

	header, err := readDefinitionHeader(kindRiskType, text, fmData, opts)
	if err != nil {
		return nil, err
	}
	desc, sections, order := extractSectionsWithDesc(text)

	risk := &BknRiskType{
		BknRiskTypeFrontmatter: BknRiskTypeFrontmatter{
			Type: "risk_type",
			ID:   header.id,
			Name: header.name,
			Tags: header.tags,
		},
		Description: header.description(buildDescription(desc, sections, order, knownRiskTypeSections)),
		RawContent:  text,
	}
	risk.Summary = ExtractSummary(risk.Description)

	return risk, nil
}

// ParseConceptGroupFile parses a concept_group definition file by the 2.x rules.
func ParseConceptGroupFile(text string, sourcePath string) (*BknConceptGroup, error) {
	return parseConceptGroupFile(text, sourcePath, parseOptions{})
}

func parseConceptGroupFile(text string, sourcePath string, opts parseOptions) (*BknConceptGroup, error) {
	fmData, err := ParseFrontmatter(text)
	if err != nil {
		return nil, err
	}
	header, err := readDefinitionHeader(kindConceptGroup, text, fmData, opts)
	if err != nil {
		return nil, err
	}

	desc, sections, order := extractSectionsWithDesc(text)

	cg := &BknConceptGroup{
		BknConceptGroupFrontmatter: BknConceptGroupFrontmatter{
			Type: "concept_group",
			ID:   header.id,
			Name: header.name,
			Tags: header.tags,
		},
		Description: header.description(buildDescription(desc, sections, order, knownConceptGroupSections)),
		RawContent:  text,
	}
	cg.Summary = ExtractSummary(cg.Description)

	if s, ok := sections["Object Types"]; ok {
		cg.ObjectTypes = parseConceptGroupObjectTypes(s)
	}

	return cg, nil
}

// firstNonEmpty returns the first non-empty value found in row under the given keys.
func firstNonEmpty(row map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := row[k]; v != "" {
			return v
		}
	}
	return ""
}

// parseConceptGroupObjectTypes parses the object types list for a concept group.
// Supports both table format and list format.
func parseConceptGroupObjectTypes(sectionText string) []string {
	// Try table format first
	rows := parseTable(strings.Split(sectionText, "\n"))

	var objectTypes []string
	if len(rows) > 0 {
		for _, row := range rows {
			// Check various possible column names for object type ID
			if id := row["ID"]; id != "" {
				objectTypes = append(objectTypes, id)
			}
		}
	}

	return objectTypes
}

// parseCapabilities reads the capability dependency section out of already-parsed frontmatter.
//
// A malformed section yields nil rather than an error: the section is a dependency declaration,
// and a network whose model is otherwise valid should still import — with its capabilities
// reported as unresolvable — instead of being rejected outright. Frontmatter is read as a map, so
// a reader that predates this section ignores it for free.
func parseCapabilities(fmData map[string]any) *BknCapabilities {
	raw, ok := fmData["capabilities"]
	if !ok || raw == nil {
		return nil
	}
	encoded, err := yaml.Marshal(raw)
	if err != nil {
		return nil
	}
	capabilities := &BknCapabilities{}
	if err := yaml.Unmarshal(encoded, capabilities); err != nil {
		return nil
	}
	if len(capabilities.Skills) == 0 && len(capabilities.Functions) == 0 &&
		len(capabilities.MCPTools) == 0 {
		return nil
	}
	return capabilities
}

// definitionHeader is what every definition file reads from its frontmatter, by the rules of the
// package's specification.
type definitionHeader struct {
	id, name string
	tags     []string
	aliases  []string
	v3       bool
	raw      rawFrontmatter
}

// readDefinitionHeader reads id, name and tags. Under 3.0 it first applies the structure rules of
// strict.go to the whole file and reads the fields as written.
func readDefinitionHeader(kind, text string, fmData map[string]any, opts parseOptions) (definitionHeader, error) {
	if !opts.v3() {
		return definitionHeader{id: strVal(fmData, "id"), name: strVal(fmData, "name"), tags: strSliceVal(fmData, "tags")}, nil
	}
	raw, err := parseRawFrontmatter(text)
	if err != nil {
		return definitionHeader{}, err
	}
	if err := checkDefinitionV3(kind, text, raw); err != nil {
		return definitionHeader{}, err
	}
	aliases, err := parseAliasesNode(raw.values["aliases"])
	if err != nil {
		return definitionHeader{}, err
	}
	return definitionHeader{id: raw.str("id"), name: raw.str("name"), tags: raw.list("tags"), aliases: aliases, v3: true, raw: raw}, nil
}

// description returns a definition's description as stored: under 3.0 the exporter's escape of
// lines that start with # is reversed.
func (h definitionHeader) description(desc string) string {
	if h.v3 {
		return unescapeDescriptionV3(desc)
	}
	return desc
}
