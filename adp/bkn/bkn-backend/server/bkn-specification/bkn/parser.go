// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"fmt"
	"regexp"
	"strings"

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
	"Mapping View":     true,
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

var knownMetricSections = map[string]bool{
	"Metric attributes":   true,
	"Scope":               true,
	"Calculation Formula": true,
	"Time Dimension":      true,
	"Analysis Dimensions": true,
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

func splitRow(row string) []string {
	row = strings.TrimSpace(row)
	row = strings.TrimPrefix(row, "|")
	row = strings.TrimSuffix(row, "|")
	parts := strings.Split(row, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func parseTable(lines []string) []map[string]string {
	var tableLines []string
	for _, line := range lines {
		s := strings.TrimSpace(line)
		if strings.HasPrefix(s, "|") {
			tableLines = append(tableLines, s)
		} else if len(tableLines) > 0 {
			break
		}
	}
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

func parseDataProperties(sectionText string) []*DataProperty {
	rows := parseTable(strings.Split(sectionText, "\n"))
	var props []*DataProperty
	for _, row := range rows {
		props = append(props, &DataProperty{
			Name:        row["Name"],
			DisplayName: row["Display Name"],
			Type:        row["Type"],
			Description: row["Description"],
			MappedField: row["Mapped Field"],
		})
	}
	return props
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
					Type: rows[0]["Source Type"],
					ID:   rows[0]["Source ID"],
					Name: rows[0]["Source Name"],
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
		if strings.HasPrefix(trimmed, "|") {
			tableLines = append(tableLines, trimmed)
		}
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

	network := &BknNetwork{
		BknNetworkFrontmatter: BknNetworkFrontmatter{
			Type:           strVal(fmData, "type"),
			ID:             strVal(fmData, "id"),
			Name:           strVal(fmData, "name"),
			Tags:           strSliceVal(fmData, "tags"),
			Version:        strVal(fmData, "version"),
			Branch:         strVal(fmData, "branch"),
			BusinessDomain: strVal(fmData, "business_domain"),
		},
		Description: extractBodyDescription(text),
		RawContent:  text,
	}
	network.Summary = ExtractSummary(network.Description)

	return network, nil
}

// ParseObjectTypeFile parses an object_type definition file.
func ParseObjectTypeFile(text string, sourcePath string) (*BknObjectType, error) {
	fmData, err := ParseFrontmatter(text)
	if err != nil {
		return nil, err
	}

	desc, sections, order := extractSectionsWithDesc(text)

	obj := &BknObjectType{
		BknObjectTypeFrontmatter: BknObjectTypeFrontmatter{
			Type: "object_type",
			ID:   strVal(fmData, "id"),
			Name: strVal(fmData, "name"),
			Tags: strSliceVal(fmData, "tags"),
		},
		Description: buildDescription(desc, sections, order, knownObjectTypeSections),
		RawContent:  text,
	}
	obj.Summary = ExtractSummary(obj.Description)

	_, obj.HasDataPropertiesSection = sections["Data Properties"]
	_, obj.HasKeysSection = sections["Keys"]

	if s, ok := sections["Data Source"]; ok {
		obj.DataSource = parseDataSource(s)
	}
	if s, ok := sections["Data Properties"]; ok {
		obj.DataProperties = parseDataProperties(s)
	}
	if s, ok := sections["Logic Properties"]; ok {
		obj.LogicProperties = parseLogicProperties(s)
	}
	if s, ok := sections["Keys"]; ok {
		pks, dk, ik := parseKeys(s)
		obj.PrimaryKeys = pks
		obj.DisplayKey = dk
		obj.IncrementalKey = ik
	}

	return obj, nil
}

// ParseRelationTypeFile parses a relation_type definition file.
func ParseRelationTypeFile(text string, sourcePath string) (*BknRelationType, error) {
	fmData, err := ParseFrontmatter(text)
	if err != nil {
		return nil, err
	}

	desc, sections, order := extractSectionsWithDesc(text)

	rel := &BknRelationType{
		BknRelationTypeFrontmatter: BknRelationTypeFrontmatter{
			Type: "relation_type",
			ID:   strVal(fmData, "id"),
			Name: strVal(fmData, "name"),
			Tags: strSliceVal(fmData, "tags"),
		},
		Description: buildDescription(desc, sections, order, knownRelationTypeSections),
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
	case RELATION_MAPPING_TYPE_FILTERED_CROSS_JOIN:
		mapping := &FilteredCrossJoinMapping{}
		if s, ok := sections["Source Condition"]; ok {
			mapping.SourceCondition = parseCondition(s)
		}
		if s, ok := sections["Target Condition"]; ok {
			mapping.TargetCondition = parseCondition(s)
		}
		rel.MappingRules = mapping
	case RELATION_MAPPING_TYPE_DATA_VIEW:
		indirect := &InDirectMappingRule{}
		if s, ok := sections["Mapping View"]; ok {
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
				sp, vp := row["Source Property"], row["View Property"]
				if sp != "" || vp != "" {
					indirect.SourceMappingRules = append(indirect.SourceMappingRules, MappingRule{SourceProperty: sp, TargetProperty: vp})
				}
			}
		}
		if s, ok := sections["Target Mapping"]; ok {
			rows := parseTable(strings.Split(s, "\n"))
			for _, row := range rows {
				vp, tp := row["View Property"], row["Target Property"]
				if vp != "" || tp != "" {
					indirect.TargetMappingRules = append(indirect.TargetMappingRules, MappingRule{SourceProperty: vp, TargetProperty: tp})
				}
			}
		}
		rel.MappingRules = indirect
	}

	return rel, nil
}

// ParseActionTypeFile parses an action_type definition file.
func ParseActionTypeFile(text string, sourcePath string) (*BknActionType, error) {
	fmData, err := ParseFrontmatter(text)
	if err != nil {
		return nil, err
	}

	desc, sections, order := extractSectionsWithDesc(text)

	act := &BknActionType{
		BknActionTypeFrontmatter: BknActionTypeFrontmatter{
			Type:         "action_type",
			ID:           strVal(fmData, "id"),
			Name:         strVal(fmData, "name"),
			Tags:         strSliceVal(fmData, "tags"),
			ActionType:   strVal(fmData, "action_type"),
			ActionIntent: strVal(fmData, "action_intent"),
		},
		Description: buildDescription(desc, sections, order, knownActionTypeSections),
		RawContent:  text,
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
		act.Parameters = parseParameterBinding(s)
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
func parseParameterBinding(sectionText string) []Parameter {
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
		params = append(params, param)
	}
	return params
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

// ParseRiskTypeFile parses a risk_type definition file.
func ParseRiskTypeFile(text string, sourcePath string) (*BknRiskType, error) {
	fmData, err := ParseFrontmatter(text)
	if err != nil {
		return nil, err
	}

	desc, sections, order := extractSectionsWithDesc(text)

	risk := &BknRiskType{
		BknRiskTypeFrontmatter: BknRiskTypeFrontmatter{
			Type: "risk_type",
			ID:   strVal(fmData, "id"),
			Name: strVal(fmData, "name"),
			Tags: strSliceVal(fmData, "tags"),
		},
		Description: buildDescription(desc, sections, order, knownRiskTypeSections),
		RawContent:  text,
	}
	risk.Summary = ExtractSummary(risk.Description)

	return risk, nil
}

// extractFirstMetricFormulaYAML returns the inner YAML of the first ```yaml fence in Calculation Formula body.
func extractFirstMetricFormulaYAML(sectionText string) []byte {
	m := yamlBlockRE.FindStringSubmatch(sectionText)
	if len(m) < 2 {
		return nil
	}
	return []byte(strings.TrimSpace(m[1]))
}

func parseMetricAttributes(sectionText string) MetricAttributes {
	rows := parseTable(strings.Split(sectionText, "\n"))
	if len(rows) == 0 {
		return MetricAttributes{}
	}

	r0 := rows[0]
	return MetricAttributes{
		MetricType: strings.TrimSpace(firstNonEmpty(r0, "Metric Type", "MetricType", "指标类型")),
		UnitType:   strings.TrimSpace(firstNonEmpty(r0, "Unit Type", "UnitType", "单位类型")),
		Unit:       strings.TrimSpace(firstNonEmpty(r0, "Unit", "度量单位")),
	}
}

func parseMetricScope(sectionText string) (scopeType, scopeRef string) {
	rows := parseTable(strings.Split(sectionText, "\n"))
	if len(rows) == 0 {
		return "", ""
	}
	r := rows[0]
	return strings.TrimSpace(r["Scope Type"]), strings.TrimSpace(r["Scope Ref"])
}

func parseMetricTimeDimensions(sectionText string) []MetricTimeDimRow {
	rows := parseTable(strings.Split(sectionText, "\n"))
	var out []MetricTimeDimRow
	for _, row := range rows {
		prop := firstNonEmpty(row, "Property")
		pol := firstNonEmpty(row, "Default Range Policy")
		if prop != "" || pol != "" {
			out = append(out, MetricTimeDimRow{Property: prop, Policy: pol})
		}
	}
	return out
}

func parseMetricAnalysisDimensions(sectionText string) []MetricAnalysisDimRow {
	rows := parseTable(strings.Split(sectionText, "\n"))
	var out []MetricAnalysisDimRow
	for _, row := range rows {
		n := row["Name"]
		dn := firstNonEmpty(row, "Display Name", "DisplayName")
		if n != "" || dn != "" {
			out = append(out, MetricAnalysisDimRow{Name: n, DisplayName: dn})
		}
	}
	return out
}

// ParseMetricFile parses a network-level metric file (metrics/*.bkn).
func ParseMetricFile(text string, sourcePath string) (*BknMetric, error) {
	fmData, err := ParseFrontmatter(text)
	if err != nil {
		return nil, err
	}

	desc, sections, order := extractSectionsWithDesc(text)

	m := &BknMetric{
		BknMetricFrontmatter: BknMetricFrontmatter{
			Type: strVal(fmData, "type"),
			ID:   strVal(fmData, "id"),
			Name: strVal(fmData, "name"),
			Tags: strSliceVal(fmData, "tags"),
		},
		Description: buildDescription(desc, sections, order, knownMetricSections),
		RawContent:  text,
	}
	if strings.TrimSpace(m.Type) == "" {
		m.Type = "metric"
	}
	m.Summary = ExtractSummary(m.Description)

	_, m.HasScopeSection = sections["Scope"]
	_, m.HasCalculationFormulaSection = sections["Calculation Formula"]
	_, m.HasTimeDimensionSection = sections["Time Dimension"]
	_, m.HasAnalysisDimensionsSection = sections["Analysis Dimensions"]
	if _, ok := sections["Metric attributes"]; ok {
		m.HasMetricAttributesSection = true
	}

	if s, ok := sections["Metric attributes"]; ok {
		m.MetricAttributes = parseMetricAttributes(s)
	}

	if s, ok := sections["Scope"]; ok {
		st, sr := parseMetricScope(s)
		m.ScopeType, m.ScopeRef = st, sr
	}
	if s, ok := sections["Calculation Formula"]; ok {
		yamlBytes := extractFirstMetricFormulaYAML(s)
		if len(yamlBytes) > 0 {
			var f MetricFormula
			if err := yaml.Unmarshal(yamlBytes, &f); err != nil {
				return nil, fmt.Errorf("calculation formula yaml: %w", err)
			}
			m.Formula = &f
		}
	}
	if s, ok := sections["Time Dimension"]; ok {
		m.TimeDimensions = parseMetricTimeDimensions(s)
	}
	if s, ok := sections["Analysis Dimensions"]; ok {
		m.AnalysisDimensions = parseMetricAnalysisDimensions(s)
	}

	// If Metric Type column empty, derive from authoritative formula kind.
	if strings.TrimSpace(m.MetricAttributes.MetricType) == "" && m.Formula != nil && strings.TrimSpace(m.Formula.Kind) != "" {
		m.MetricAttributes.MetricType = strings.TrimSpace(m.Formula.Kind)
	}

	return m, nil
}

func ParseConceptGroupFile(text string, sourcePath string) (*BknConceptGroup, error) {
	fmData, err := ParseFrontmatter(text)
	if err != nil {
		return nil, err
	}

	desc, sections, order := extractSectionsWithDesc(text)

	cg := &BknConceptGroup{
		BknConceptGroupFrontmatter: BknConceptGroupFrontmatter{
			Type: "concept_group",
			ID:   strVal(fmData, "id"),
			Name: strVal(fmData, "name"),
			Tags: strSliceVal(fmData, "tags"),
		},
		Description: buildDescription(desc, sections, order, knownConceptGroupSections),
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
