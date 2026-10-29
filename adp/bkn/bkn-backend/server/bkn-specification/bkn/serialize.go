// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common/maskrule"

	"gopkg.in/yaml.v3"
)

func serializeMaskRule(rule *maskrule.Rule) string {
	if rule == nil {
		return ""
	}
	data, err := json.Marshal(rule)
	if err != nil {
		return ""
	}
	// A literal pipe would terminate the Markdown table cell. Its JSON escape
	// decodes to the same replacement string on the next import.
	return strings.ReplaceAll(string(data), "|", `\u007c`)
}

// tableCell makes a value safe inside a Markdown table row. A line break would end the row and
// a bare pipe would open a new cell; the importer's table reader can recover neither, so the
// break is written as <br> and the pipe as \|, and splitRow turns them back.
func tableCell(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "|", `\|`)
	// A literal <br> in the value must not read back as a line break, so it is entity-encoded
	// before the real line breaks take the <br> spelling.
	s = strings.ReplaceAll(s, "<br>", "&lt;br&gt;")
	return strings.ReplaceAll(s, "\n", "<br>")
}

// hasToolSourceColumns reports whether a logical-property source needs tool addressing columns.
// Serialization deliberately remains lossless for documents that validation may reject; this is
// separate from the business rule that only tool-backed logical properties are valid.
func hasToolSourceColumns(src *ResourceInfo) bool {
	if src == nil {
		return false
	}
	return normType(src.Type) == "tool" || src.BoxID != "" || src.ToolID != "" || src.ResultPath != ""
}

// encodeYAMLBlock encodes v to YAML and wraps it in a ```yaml code fence.
func encodeYAMLBlock(v any) string {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	_ = enc.Encode(v)
	_ = enc.Close()
	return "```yaml\n" + buf.String() + "```\n"
}

// SerializeBknNetwork Serializes BknNetwork to BKN format
func SerializeBknNetwork(doc *BknNetwork) string {
	return serializeBknNetwork(doc, serializeOptions{})
}

func serializeBknNetwork(doc *BknNetwork, opts serializeOptions) string {
	var sb strings.Builder
	if opts.v3 {
		fields := []frontmatterField{
			scalarField("type", "knowledge_network"), scalarField("id", doc.ID), scalarField("name", doc.Name),
			listField("tags", doc.Tags), scalarField("bkn_spec", BknSpecV3),
		}
		if doc.Version != "" {
			fields = append(fields, scalarField("version", doc.Version))
		}
		if doc.Branch != "" {
			fields = append(fields, scalarField("branch", doc.Branch))
		}
		if capabilities := capabilitiesField(doc.Capabilities); capabilities != nil {
			fields = append(fields, *capabilities)
		}
		writeFrontmatterV3(&sb, fields)
	} else {
		_, _ = fmt.Fprintf(&sb, "---\n")
		_, _ = fmt.Fprintf(&sb, "type: knowledge_network\n")
		_, _ = fmt.Fprintf(&sb, "id: %s\n", doc.ID)
		_, _ = fmt.Fprintf(&sb, "name: %s\n", doc.Name)
		_, _ = fmt.Fprintf(&sb, "tags: [%s]\n", strings.Join(doc.Tags, ", "))

		if doc.Version != "" {
			_, _ = fmt.Fprintf(&sb, "version: %s\n", doc.Version)
		}
		if doc.Branch != "" {
			_, _ = fmt.Fprintf(&sb, "branch: %s\n", doc.Branch)
		}
		if block := serializeCapabilities(doc.Capabilities); block != "" {
			_, _ = fmt.Fprint(&sb, block)
		}
		_, _ = fmt.Fprintf(&sb, "---\n\n")
	}

	_, _ = fmt.Fprintf(&sb, "# %s\n\n", doc.Name)
	if doc.Description != "" {
		_, _ = fmt.Fprintf(&sb, "%s\n\n", opts.description(doc.Description))
	}

	// Network Overview
	_, _ = fmt.Fprintf(&sb, "\n## Network Overview\n")

	_, _ = fmt.Fprintf(&sb, "\n### Object Types\n\n")
	_, _ = fmt.Fprintf(&sb, "| ID | Name | File Path | Description |\n")
	_, _ = fmt.Fprintf(&sb, "|----|------|-----------|-------------|\n")
	if len(doc.ObjectTypes) > 0 {
		sort.Slice(doc.ObjectTypes, func(i, j int) bool { return doc.ObjectTypes[i].ID < doc.ObjectTypes[j].ID })
		for _, ot := range doc.ObjectTypes {
			_, _ = fmt.Fprintf(&sb, "| %s | %s | `object_types/%s.bkn` | %s |\n", tableCell(ot.ID), tableCell(ot.Name), tableCell(ot.ID), tableCell(ot.Summary))
		}
	}

	_, _ = fmt.Fprintf(&sb, "\n### Relation Types\n\n")
	_, _ = fmt.Fprintf(&sb, "| ID | Name | File Path | Description |\n")
	_, _ = fmt.Fprintf(&sb, "|----|------|-----------|-------------|\n")
	if len(doc.RelationTypes) > 0 {
		sort.Slice(doc.RelationTypes, func(i, j int) bool { return doc.RelationTypes[i].ID < doc.RelationTypes[j].ID })
		for _, rt := range doc.RelationTypes {
			fmt.Fprintf(&sb, "| %s | %s | `relation_types/%s.bkn` | %s |\n", tableCell(rt.ID), tableCell(rt.Name), tableCell(rt.ID), tableCell(rt.Summary))
		}
	}

	_, _ = fmt.Fprintf(&sb, "\n### Action Types\n\n")
	_, _ = fmt.Fprintf(&sb, "| ID | Name | File Path | Description |\n")
	_, _ = fmt.Fprintf(&sb, "|----|------|-----------|-------------|\n")
	if len(doc.ActionTypes) > 0 {
		sort.Slice(doc.ActionTypes, func(i, j int) bool { return doc.ActionTypes[i].ID < doc.ActionTypes[j].ID })
		for _, at := range doc.ActionTypes {
			fmt.Fprintf(&sb, "| %s | %s | `action_types/%s.bkn` | %s |\n", tableCell(at.ID), tableCell(at.Name), tableCell(at.ID), tableCell(at.Summary))
		}
	}

	_, _ = fmt.Fprintf(&sb, "\n### Risk Types\n\n")
	_, _ = fmt.Fprintf(&sb, "| ID | Name | File Path | Description |\n")
	_, _ = fmt.Fprintf(&sb, "|----|------|-----------|-------------|\n")
	if len(doc.RiskTypes) > 0 {
		sort.Slice(doc.RiskTypes, func(i, j int) bool { return doc.RiskTypes[i].ID < doc.RiskTypes[j].ID })
		for _, rt := range doc.RiskTypes {
			fmt.Fprintf(&sb, "| %s | %s | `risk_types/%s.bkn` | %s |\n", tableCell(rt.ID), tableCell(rt.Name), tableCell(rt.ID), tableCell(rt.Summary))
		}
	}

	_, _ = fmt.Fprintf(&sb, "\n### Concept Groups\n\n")
	_, _ = fmt.Fprintf(&sb, "| ID | Name | File Path | Description |\n")
	_, _ = fmt.Fprintf(&sb, "|----|------|-----------|-------------|\n")
	if len(doc.ConceptGroups) > 0 {
		sort.Slice(doc.ConceptGroups, func(i, j int) bool { return doc.ConceptGroups[i].ID < doc.ConceptGroups[j].ID })
		for _, cg := range doc.ConceptGroups {
			fmt.Fprintf(&sb, "| %s | %s | `concept_groups/%s.bkn` | %s |\n", tableCell(cg.ID), tableCell(cg.Name), tableCell(cg.ID), tableCell(cg.Summary))
		}
	}

	if opts.v3 {
		sb.WriteString("\n### Value Types\n\n")
		sb.WriteString("| ID | Name | File Path | Description |\n")
		sb.WriteString("|----|------|-----------|-------------|\n")
		sort.Slice(doc.ValueTypes, func(i, j int) bool { return doc.ValueTypes[i].ID < doc.ValueTypes[j].ID })
		for _, vt := range doc.ValueTypes {
			fmt.Fprintf(&sb, "| %s | %s | `value_types/%s.bkn` | %s |\n", tableCell(vt.ID), tableCell(vt.Name), tableCell(vt.ID), tableCell(vt.Summary))
		}
	}

	sb.WriteString("\n### Object Metrics V1\n\n")
	sb.WriteString("| ID | Name | File Path | Description |\n")
	sb.WriteString("|----|------|-----------|-------------|\n")
	if len(doc.ObjectMetrics) > 0 {
		for _, metric := range doc.ObjectMetrics {
			id, version, name, description := objectMetricArchiveMetadata(metric)
			fmt.Fprintf(&sb, "| %s@%d | %s | `object_metrics/%s` | %s |\n",
				tableCell(id), version, tableCell(name), tableCell(objectMetricArchiveFileName(id, version)), tableCell(description))
		}
	}

	// Directory Structure — full tree with file listings
	type dirEntry struct {
		dir   string
		files []string
	}
	var dirs []dirEntry
	if len(doc.ObjectTypes) > 0 {
		files := make([]string, len(doc.ObjectTypes))
		for i, ot := range doc.ObjectTypes {
			files[i] = ot.ID + ".bkn"
		}
		dirs = append(dirs, dirEntry{"object_types", files})
	}
	if len(doc.RelationTypes) > 0 {
		files := make([]string, len(doc.RelationTypes))
		for i, rt := range doc.RelationTypes {
			files[i] = rt.ID + ".bkn"
		}
		dirs = append(dirs, dirEntry{"relation_types", files})
	}
	if len(doc.ActionTypes) > 0 {
		files := make([]string, len(doc.ActionTypes))
		for i, at := range doc.ActionTypes {
			files[i] = at.ID + ".bkn"
		}
		dirs = append(dirs, dirEntry{"action_types", files})
	}
	if len(doc.RiskTypes) > 0 {
		files := make([]string, len(doc.RiskTypes))
		for i, rt := range doc.RiskTypes {
			files[i] = rt.ID + ".bkn"
		}
		dirs = append(dirs, dirEntry{"risk_types", files})
	}
	if len(doc.ConceptGroups) > 0 {
		files := make([]string, len(doc.ConceptGroups))
		for i, cg := range doc.ConceptGroups {
			files[i] = cg.ID + ".bkn"
		}
		dirs = append(dirs, dirEntry{"concept_groups", files})
	}
	if opts.v3 && len(doc.ValueTypes) > 0 {
		files := make([]string, len(doc.ValueTypes))
		for i, vt := range doc.ValueTypes {
			files[i] = vt.ID + ".bkn"
		}
		dirs = append(dirs, dirEntry{valueTypesDir, files})
	}
	if len(doc.ObjectMetrics) > 0 {
		files := make([]string, len(doc.ObjectMetrics))
		for i, metric := range doc.ObjectMetrics {
			id, version, _, _ := objectMetricArchiveMetadata(metric)
			files[i] = objectMetricArchiveFileName(id, version)
		}
		dirs = append(dirs, dirEntry{"object_metrics", files})
	}

	_, _ = fmt.Fprintf(&sb, "\n## Directory Structure\n\n```\n.\n├── network.bkn\n├── SKILL.md\n├── CHECKSUM\n")
	for i, d := range dirs {
		isLastDir := i == len(dirs)-1
		dirPrefix, childPrefix := "├── ", "│   "
		if isLastDir {
			dirPrefix, childPrefix = "└── ", "    "
		}
		_, _ = fmt.Fprintf(&sb, "%s%s/\n", dirPrefix, d.dir)
		for j, f := range d.files {
			if j == len(d.files)-1 {
				fmt.Fprintf(&sb, "%s└── %s\n", childPrefix, f)
			} else {
				fmt.Fprintf(&sb, "%s├── %s\n", childPrefix, f)
			}
		}
	}
	_, _ = fmt.Fprintf(&sb, "```\n")

	return sb.String()
}

// SerializeObjectType Serializes BknObjectType to BKN format
func SerializeObjectType(ot *BknObjectType) string {
	return serializeObjectType(ot, serializeOptions{})
}

func serializeObjectType(ot *BknObjectType, opts serializeOptions) string {
	var sb strings.Builder
	writeDefinitionFrontmatter(&sb, opts, "object_type", ot.ID, ot.Name, ot.Tags,
		append(aliasesField(ot.Aliases), lifecycleField(ot.Lifecycle)...)...)

	_, _ = fmt.Fprintf(&sb, "## ObjectType: %s\n\n", ot.Name)
	if ot.Description != "" {
		_, _ = fmt.Fprintf(&sb, "%s\n\n", opts.description(ot.Description))
	}

	// Data Source
	_, _ = fmt.Fprintf(&sb, "### Data Source\n\n")
	_, _ = fmt.Fprintf(&sb, "| Type | ID | Name |\n")
	_, _ = fmt.Fprintf(&sb, "|------|----|------|\n")
	if ot.DataSource != nil {
		_, _ = fmt.Fprintf(&sb, "| %s | %s | %s |\n",
			tableCell(ot.DataSource.Type), tableCell(ot.DataSource.ID), tableCell(ot.DataSource.Name))
	}
	_, _ = fmt.Fprintf(&sb, "\n")

	// Data Properties
	_, _ = fmt.Fprintf(&sb, "### Data Properties\n\n")
	if opts.v3 {
		writeDataPropertiesV3(&sb, ot.DataProperties, ot.PrimaryKeys)
	} else {
		writeDataProperties(&sb, ot)
	}

	// Logic Properties
	_, _ = fmt.Fprintf(&sb, "### Logic Properties\n\n")
	for _, lp := range ot.LogicProperties {
		_, _ = fmt.Fprintf(&sb, "#### %s\n\n", lp.Name)

		// Meta table
		_, _ = fmt.Fprintf(&sb, "**Meta**\n\n")
		_, _ = fmt.Fprintf(&sb, "| Display Name | Type | Description |\n")
		_, _ = fmt.Fprintf(&sb, "|--------------|------|-------------|\n")
		_, _ = fmt.Fprintf(&sb, "| %s | %s | %s |\n\n", tableCell(lp.DisplayName), tableCell(lp.Type), tableCell(lp.Description))

		// Tool sources carry their addressing columns. The narrow form is retained only
		// so serialization does not rewrite documents that validation may reject.
		_, _ = fmt.Fprintf(&sb, "**Source**\n\n")
		if hasToolSourceColumns(lp.DataSource) {
			_, _ = fmt.Fprintf(&sb, "| Source Type | Source ID | Source Name | BoxID | ToolID | ResultPath |\n")
			_, _ = fmt.Fprintf(&sb, "|-------------|-----------|-------------|-------|--------|------------|\n")
			_, _ = fmt.Fprintf(&sb, "| %s | %s | %s | %s | %s | %s |\n",
				tableCell(lp.DataSource.Type), tableCell(lp.DataSource.ID), tableCell(lp.DataSource.Name),
				tableCell(lp.DataSource.BoxID), tableCell(lp.DataSource.ToolID), tableCell(lp.DataSource.ResultPath))
		} else {
			_, _ = fmt.Fprintf(&sb, "| Source Type | Source ID | Source Name |\n")
			_, _ = fmt.Fprintf(&sb, "|-------------|-----------|-------------|\n")
			if lp.DataSource != nil {
				_, _ = fmt.Fprintf(&sb, "| %s | %s | %s |\n", tableCell(lp.DataSource.Type), tableCell(lp.DataSource.ID), tableCell(lp.DataSource.Name))
			}
		}
		_, _ = fmt.Fprintf(&sb, "\n")

		// Parameter table
		_, _ = fmt.Fprintf(&sb, "**Parameters**\n\n")
		_, _ = fmt.Fprintf(&sb, "| Name | Type | Source | Operation | ValueFrom | Value | Description |\n")
		_, _ = fmt.Fprintf(&sb, "|------|------|--------|-----------|-----------|-------|-------------|\n")
		for _, p := range lp.Parameters {
			v := ""
			if p.Value != nil {
				v = fmt.Sprintf("%v", p.Value)
			}
			_, _ = fmt.Fprintf(&sb, "| %s | %s | %s | %s | %s | %s | %s |\n",
				tableCell(p.Name), tableCell(p.Type), tableCell(p.Source), tableCell(p.Operation), tableCell(p.ValueFrom), tableCell(v), tableCell(p.Description))
		}
		_, _ = fmt.Fprintf(&sb, "\n")

		// Analysis Dims table
		_, _ = fmt.Fprintf(&sb, "**Analysis Dimensions**\n\n")
		_, _ = fmt.Fprintf(&sb, "| Name | Display Name | Type | Description |\n")
		_, _ = fmt.Fprintf(&sb, "|------|--------------|------|-------------|\n")
		for _, d := range lp.AnalysisDims {
			_, _ = fmt.Fprintf(&sb, "| %s | %s | %s | %s |\n", tableCell(d.Name), tableCell(d.DisplayName), tableCell(d.Type), tableCell(d.Description))
		}
		_, _ = fmt.Fprintf(&sb, "\n")
	}
	_, _ = fmt.Fprintf(&sb, "\n")

	// Keys section
	_, _ = fmt.Fprintf(&sb, "### Keys\n\n")
	_, _ = fmt.Fprintf(&sb, "Primary Keys: %s\n", strings.Join(ot.PrimaryKeys, ", "))
	_, _ = fmt.Fprintf(&sb, "Display Key: %s\n", ot.DisplayKey)
	_, _ = fmt.Fprintf(&sb, "Incremental Key: %s\n", ot.IncrementalKey)
	_, _ = fmt.Fprintf(&sb, "\n")

	return sb.String()
}

// writeDataProperties writes the 2.x Data Properties table.
func writeDataProperties(sb *strings.Builder, ot *BknObjectType) {
	hasMaskRule := false
	for _, dp := range ot.DataProperties {
		if dp.MaskRule != nil {
			hasMaskRule = true
			break
		}
	}
	if hasMaskRule {
		_, _ = fmt.Fprintf(sb, "| Name | Display Name | Type | Description | Mapped Field | Mask Rule |\n")
		_, _ = fmt.Fprintf(sb, "|------|--------------|------|-------------|--------------|-----------|\n")
	} else {
		_, _ = fmt.Fprintf(sb, "| Name | Display Name | Type | Description | Mapped Field |\n")
		_, _ = fmt.Fprintf(sb, "|------|--------------|------|-------------|--------------|\n")
	}
	if len(ot.DataProperties) > 0 {
		for _, dp := range ot.DataProperties {
			if hasMaskRule {
				_, _ = fmt.Fprintf(sb, "| %s | %s | %s | %s | %s | %s |\n",
					tableCell(dp.Name), tableCell(dp.DisplayName), tableCell(dp.Type), tableCell(dp.Description), tableCell(dp.MappedField), tableCell(serializeMaskRule(dp.MaskRule)))
			} else {
				_, _ = fmt.Fprintf(sb, "| %s | %s | %s | %s | %s |\n",
					tableCell(dp.Name), tableCell(dp.DisplayName), tableCell(dp.Type), tableCell(dp.Description), tableCell(dp.MappedField))
			}
		}
	}
	_, _ = fmt.Fprintf(sb, "\n")
}

// SerializeRelationType Serializes BknRelationType to BKN format
func SerializeRelationType(rt *BknRelationType) string {
	return serializeRelationType(rt, serializeOptions{})
}

func serializeRelationType(rt *BknRelationType, opts serializeOptions) string {
	var sb strings.Builder
	var extra []frontmatterField
	if opts.v3 {
		extra = relationFields(rt)
	}
	writeDefinitionFrontmatter(&sb, opts, "relation_type", rt.ID, rt.Name, rt.Tags, extra...)

	_, _ = fmt.Fprintf(&sb, "## RelationType: %s\n\n", rt.Name)
	if rt.Description != "" {
		_, _ = fmt.Fprintf(&sb, "%s\n\n", opts.description(rt.Description))
	}

	// Endpoint
	_, _ = fmt.Fprintf(&sb, "### Endpoint\n\n")
	if opts.v3 && rt.Endpoint.Cardinality != "" {
		_, _ = fmt.Fprintf(&sb, "| Source | Target | Type | Cardinality |\n")
		_, _ = fmt.Fprintf(&sb, "|--------|--------|------|-------------|\n")
		_, _ = fmt.Fprintf(&sb, "| %s | %s | %s | %s |\n\n", tableCell(rt.Endpoint.Source), tableCell(rt.Endpoint.Target),
			tableCell(rt.Endpoint.Type), tableCell(rt.Endpoint.Cardinality))
	} else {
		_, _ = fmt.Fprintf(&sb, "| Source | Target | Type |\n")
		_, _ = fmt.Fprintf(&sb, "|--------|--------|------|\n")
		_, _ = fmt.Fprintf(&sb, "| %s | %s | %s |\n\n", tableCell(rt.Endpoint.Source), tableCell(rt.Endpoint.Target), tableCell(rt.Endpoint.Type))
	}
	if opts.v3 {
		writeRelationConstraintsV3(&sb, rt)
	}

	switch rt.Endpoint.Type {
	case RELATION_MAPPING_TYPE_DIRECT:
		// ### Mapping Rules — simple source→target property table
		_, _ = fmt.Fprintf(&sb, "### Mapping Rules\n\n")
		_, _ = fmt.Fprintf(&sb, "| Source Property | Target Property |\n")
		_, _ = fmt.Fprintf(&sb, "|-----------------|-----------------|\n")
		if rules, ok := rt.MappingRules.(DirectMappingRule); ok {
			for _, r := range rules {
				_, _ = fmt.Fprintf(&sb, "| %s | %s |\n", tableCell(r.SourceProperty), tableCell(r.TargetProperty))
			}
		}
		_, _ = fmt.Fprintf(&sb, "\n")

	case RELATION_MAPPING_TYPE_INDIRECT:
		_, _ = fmt.Fprintf(&sb, "### Backing Resource\n\n")
		_, _ = fmt.Fprintf(&sb, "| Type | ID |\n")
		_, _ = fmt.Fprintf(&sb, "|------|----|\n")
		if rules, ok := rt.MappingRules.(*InDirectMappingRule); ok {
			if rules.BackingDataSource != nil {
				_, _ = fmt.Fprintf(&sb, "| %s | %s |\n", tableCell(rules.BackingDataSource.Type), tableCell(rules.BackingDataSource.ID))
			}
			_, _ = fmt.Fprintf(&sb, "\n")
			_, _ = fmt.Fprintf(&sb, "### Source Mapping\n\n")
			_, _ = fmt.Fprintf(&sb, "| Source Property | Resource Property |\n")
			_, _ = fmt.Fprintf(&sb, "|-----------------|-------------------|\n")
			for _, r := range rules.SourceMappingRules {
				_, _ = fmt.Fprintf(&sb, "| %s | %s |\n", tableCell(r.SourceProperty), tableCell(r.TargetProperty))
			}
			_, _ = fmt.Fprintf(&sb, "\n")
			_, _ = fmt.Fprintf(&sb, "### Target Mapping\n\n")
			_, _ = fmt.Fprintf(&sb, "| Resource Property | Target Property |\n")
			_, _ = fmt.Fprintf(&sb, "|-------------------|-----------------|\n")
			for _, r := range rules.TargetMappingRules {
				_, _ = fmt.Fprintf(&sb, "| %s | %s |\n", tableCell(r.SourceProperty), tableCell(r.TargetProperty))
			}
			_, _ = fmt.Fprintf(&sb, "\n")
		}
		if opts.v3 && len(rt.DataProperties) > 0 {
			_, _ = fmt.Fprintf(&sb, "### Data Properties\n\n")
			writeDataPropertiesV3(&sb, rt.DataProperties, nil)
		}

	case RELATION_MAPPING_TYPE_FILTERED_CROSS_JOIN:
		if fcj, ok := rt.MappingRules.(*FilteredCrossJoinMapping); ok {
			_, _ = fmt.Fprintf(&sb, "### Source Condition\n\n")
			if fcj.SourceCondition != nil {
				_, _ = fmt.Fprintf(&sb, "%s\n", encodeYAMLBlock(fcj.SourceCondition))
			}
			_, _ = fmt.Fprintf(&sb, "\n")

			_, _ = fmt.Fprintf(&sb, "### Target Condition\n\n")
			if fcj.TargetCondition != nil {
				_, _ = fmt.Fprintf(&sb, "%s\n", encodeYAMLBlock(fcj.TargetCondition))
			}
			_, _ = fmt.Fprintf(&sb, "\n")
		}
	}

	return sb.String()
}

// SerializeActionType Serializes BknActionType to BKN format
func SerializeActionType(at *BknActionType) string {
	return serializeActionType(at, serializeOptions{})
}

func serializeActionType(at *BknActionType, opts serializeOptions) string {
	var sb strings.Builder
	intent := strings.TrimSpace(at.ActionIntent)
	actionType := strings.TrimSpace(at.ActionType)
	if intent == "" && actionType != "" {
		intent = actionType
	}

	if opts.v3 {
		// 3.0 writes the intent once, as action_intent; action_type is a 2.x synonym.
		fields := []frontmatterField{
			scalarField("type", "action_type"), scalarField("id", at.ID), scalarField("name", at.Name), listField("tags", at.Tags),
		}
		fields = append(fields, aliasesField(at.Aliases)...)
		if intent != "" {
			fields = append(fields, scalarField("action_intent", intent))
		}
		fields = append(fields, lifecycleField(at.Lifecycle)...)
		writeFrontmatterV3(&sb, fields)
	} else {
		_, _ = fmt.Fprintf(&sb, "---\n")
		_, _ = fmt.Fprintf(&sb, "type: action_type\n")
		_, _ = fmt.Fprintf(&sb, "id: %s\n", at.ID)
		_, _ = fmt.Fprintf(&sb, "name: %s\n", at.Name)
		_, _ = fmt.Fprintf(&sb, "tags: [%s]\n", strings.Join(at.Tags, ", "))
		if actionType != "" {
			_, _ = fmt.Fprintf(&sb, "action_type: %s\n", actionType)
		}
		if intent != "" {
			_, _ = fmt.Fprintf(&sb, "action_intent: %s\n", intent)
		}
		_, _ = fmt.Fprintf(&sb, "---\n\n")
	}

	_, _ = fmt.Fprintf(&sb, "## ActionType: %s\n\n", at.Name)
	if at.Description != "" {
		_, _ = fmt.Fprintf(&sb, "%s\n\n", opts.description(at.Description))
	}

	// Bound Object
	_, _ = fmt.Fprintf(&sb, "### Bound Object\n\n")
	_, _ = fmt.Fprintf(&sb, "| Bound Object |\n")
	_, _ = fmt.Fprintf(&sb, "|--------------|\n")
	if at.BoundObject != "" {
		_, _ = fmt.Fprintf(&sb, "| %s |\n", tableCell(at.BoundObject))
	}
	_, _ = fmt.Fprintf(&sb, "\n")

	// Affect Object. 3.0 writes it only when it has content.
	hasAffect := at.AffectObject != nil && (at.AffectObject.ObjectType != "" || at.AffectObject.Description != "")
	if !opts.v3 || hasAffect {
		_, _ = fmt.Fprintf(&sb, "### Affect Object\n\n")
		_, _ = fmt.Fprintf(&sb, "| Affect Object | Affect Description |\n")
		_, _ = fmt.Fprintf(&sb, "|---------------|--------------------|\n")
		if at.AffectObject != nil {
			_, _ = fmt.Fprintf(&sb, "| %s | %s |\n", tableCell(at.AffectObject.ObjectType), tableCell(at.AffectObject.Description))
		}
		_, _ = fmt.Fprintf(&sb, "\n")
	}

	if len(at.ImpactContracts) > 0 {
		_, _ = fmt.Fprintf(&sb, "### Impact Contracts\n\n")
		payload := struct {
			ImpactContracts []*ImpactContractItem `yaml:"impact_contracts"`
		}{ImpactContracts: at.ImpactContracts}
		_, _ = fmt.Fprintf(&sb, "%s\n", encodeYAMLBlock(payload))
		_, _ = fmt.Fprintf(&sb, "\n")
	}

	// Trigger Condition
	_, _ = fmt.Fprintf(&sb, "### Trigger Condition\n\n")
	if at.TriggerCondition != nil {
		_, _ = fmt.Fprintf(&sb, "%s\n", encodeYAMLBlock(at.TriggerCondition))
	}
	_, _ = fmt.Fprintf(&sb, "\n")

	// Action Source
	_, _ = fmt.Fprintf(&sb, "### Action Source\n\n")
	_, _ = fmt.Fprintf(&sb, "| Type | BoxID | ToolID | McpID | ToolName |\n")
	_, _ = fmt.Fprintf(&sb, "|------|-------|--------|-------|----------|\n")
	if at.ActionSource != nil && at.ActionSource.Type != "" {
		_, _ = fmt.Fprintf(&sb, "| %s | %s | %s | %s | %s |\n",
			tableCell(at.ActionSource.Type), tableCell(at.ActionSource.BoxID), tableCell(at.ActionSource.ToolID), tableCell(at.ActionSource.McpID), tableCell(at.ActionSource.ToolName))
	}
	_, _ = fmt.Fprintf(&sb, "\n")

	// Parameter Binding
	_, _ = fmt.Fprintf(&sb, "### Parameter Binding\n\n")
	_, _ = fmt.Fprintf(&sb, "| Name | Type | Source | Operation | ValueFrom | Value | Description |\n")
	_, _ = fmt.Fprintf(&sb, "|------|------|--------|-----------|-----------|-------|-------------|\n")
	for _, p := range at.Parameters {
		v := p.Value
		if v == nil {
			v = ""
		}
		_, _ = fmt.Fprintf(&sb, "| %s | %s | %s | %s | %s | %s | %s |\n",
			tableCell(p.Name), tableCell(p.Type), tableCell(p.Source), tableCell(p.Operation), tableCell(p.ValueFrom), tableCell(fmt.Sprint(v)), tableCell(p.Description))
	}
	_, _ = fmt.Fprintf(&sb, "\n")

	// Schedule
	_, _ = fmt.Fprintf(&sb, "### Schedule\n\n")
	_, _ = fmt.Fprintf(&sb, "| Type | Expression |\n")
	_, _ = fmt.Fprintf(&sb, "|------|------------|\n")
	if at.Schedule != nil && at.Schedule.Type != "" {
		_, _ = fmt.Fprintf(&sb, "| %s | %s |\n", tableCell(at.Schedule.Type), tableCell(at.Schedule.Expression))
	}
	_, _ = fmt.Fprintf(&sb, "\n")

	return sb.String()
}

// SerializeRiskType Serializes BknRiskType to BKN format
func SerializeRiskType(rt *BknRiskType) string {
	return serializeRiskType(rt, serializeOptions{})
}

func serializeRiskType(rt *BknRiskType, opts serializeOptions) string {
	var sb strings.Builder
	writeDefinitionFrontmatter(&sb, opts, "risk_type", rt.ID, rt.Name, rt.Tags)

	_, _ = fmt.Fprintf(&sb, "## RiskType: %s\n\n", rt.Name)
	if rt.Description != "" {
		_, _ = fmt.Fprintf(&sb, "%s\n\n", opts.description(rt.Description))
	}

	return sb.String()
}

// SerializeConceptGroup Serializes BknConceptGroup to BKN format.
// otIndex maps ObjectType ID → *BknObjectType for Name/Description lookup.
func SerializeConceptGroup(cg *BknConceptGroup, otIndex map[string]*BknObjectType) string {
	return serializeConceptGroup(cg, otIndex, serializeOptions{})
}

func serializeConceptGroup(cg *BknConceptGroup, otIndex map[string]*BknObjectType, opts serializeOptions) string {
	var sb strings.Builder
	writeDefinitionFrontmatter(&sb, opts, "concept_group", cg.ID, cg.Name, cg.Tags)

	_, _ = fmt.Fprintf(&sb, "## ConceptGroup: %s\n\n", cg.Name)
	if cg.Description != "" {
		_, _ = fmt.Fprintf(&sb, "%s\n\n", opts.description(cg.Description))
	}
	_, _ = fmt.Fprintf(&sb, "### Object Types\n\n")
	_, _ = fmt.Fprintf(&sb, "| ID | Name | Description |\n")
	_, _ = fmt.Fprintf(&sb, "|----|------|-------------|\n")
	if len(cg.ObjectTypes) > 0 {
		ids := append([]string(nil), cg.ObjectTypes...)
		sort.Strings(ids)
		for _, id := range ids {
			name, desc := id, ""
			if ot, ok := otIndex[id]; ok {
				name = ot.Name
				desc = ot.Summary
			}
			_, _ = fmt.Fprintf(&sb, "| %s | %s | %s |\n", tableCell(id), tableCell(name), tableCell(desc))
		}
	}
	_, _ = fmt.Fprintf(&sb, "\n")

	return sb.String()
}

// serializeCapabilities renders the capability dependency section of the frontmatter.
//
// The nested block goes through yaml.Marshal rather than being hand-formatted like the scalar
// fields above it: nesting and quoting are exactly where hand-built YAML breaks, and a name with
// a colon in it would silently produce a file that no longer parses.
func serializeCapabilities(capabilities *BknCapabilities) string {
	if capabilities.IsEmpty() {
		return ""
	}
	encoded, err := yaml.Marshal(map[string]*BknCapabilities{"capabilities": capabilities})
	if err != nil {
		return ""
	}
	return string(encoded)
}
