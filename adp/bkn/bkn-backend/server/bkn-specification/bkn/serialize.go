// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// encodeMetricFormulaYAML encodes a metric formula fenced with Markdown ```yaml code blocks, matching on-disk examples.
func encodeMetricFormulaYAML(m *MetricFormula) string {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	_ = enc.Encode(m)
	_ = enc.Close()
	s := strings.TrimSuffix(buf.String(), "\n")
	return "```yaml\n" + s + "\n```\n"
}

// SerializeMetric serializes BknMetric to BKN markdown.
func SerializeMetric(m *BknMetric) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString("type: metric\n")
	sb.WriteString(fmt.Sprintf("id: %s\n", m.ID))
	sb.WriteString(fmt.Sprintf("name: %s\n", m.Name))
	sb.WriteString(fmt.Sprintf("tags: [%s]\n", strings.Join(m.Tags, ", ")))
	sb.WriteString("---\n\n")

	sb.WriteString(fmt.Sprintf("## Metric: %s\n\n", m.Name))
	if m.Description != "" {
		sb.WriteString(m.Description + "\n\n")
	}

	mtOut := strings.TrimSpace(m.MetricAttributes.MetricType)
	if mtOut == "" && m.Formula != nil {
		mtOut = strings.TrimSpace(m.Formula.Kind)
	}
	utOut := strings.TrimSpace(m.MetricAttributes.UnitType)
	uOut := strings.TrimSpace(m.MetricAttributes.Unit)
	if mtOut != "" || utOut != "" || uOut != "" {
		sb.WriteString("### Metric attributes\n\n")
		sb.WriteString("| Metric Type | Unit Type | Unit |\n")
		sb.WriteString("|-------------|-----------|------|\n")
		sb.WriteString(fmt.Sprintf("| %s | %s | %s |\n\n", mtOut, utOut, uOut))
	}

	sb.WriteString("### Scope\n\n")
	sb.WriteString("| Scope Type | Scope Ref |\n")
	sb.WriteString("|------------|-----------|\n")
	sb.WriteString(fmt.Sprintf("| %s | %s |\n\n", m.ScopeType, m.ScopeRef))

	sb.WriteString("### Calculation Formula\n\n")
	if m.Formula != nil {
		sb.WriteString(encodeMetricFormulaYAML(m.Formula))
		sb.WriteString("\n")
	}

	sb.WriteString("### Time Dimension\n\n")
	sb.WriteString("| Property | Default Range Policy |\n")
	sb.WriteString("|----------|------------------------|\n")
	for _, row := range m.TimeDimensions {
		sb.WriteString(fmt.Sprintf("| %s | %s |\n", row.Property, row.Policy))
	}
	sb.WriteString("\n")

	sb.WriteString("### Analysis Dimensions\n\n")
	sb.WriteString("| Name | Display Name |\n")
	sb.WriteString("|------|--------------|\n")
	for _, row := range m.AnalysisDimensions {
		sb.WriteString(fmt.Sprintf("| %s | %s |\n", row.Name, row.DisplayName))
	}
	sb.WriteString("\n")

	return sb.String()
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
	var sb strings.Builder
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
	if doc.BusinessDomain != "" {
		_, _ = fmt.Fprintf(&sb, "business_domain: %s\n", doc.BusinessDomain)
	}
	_, _ = fmt.Fprintf(&sb, "---\n\n")

	_, _ = fmt.Fprintf(&sb, "# %s\n\n", doc.Name)
	if doc.Description != "" {
		_, _ = fmt.Fprintf(&sb, "%s\n\n", doc.Description)
	}

	// Network Overview
	_, _ = fmt.Fprintf(&sb, "\n## Network Overview\n")

	_, _ = fmt.Fprintf(&sb, "\n### Object Types\n\n")
	_, _ = fmt.Fprintf(&sb, "| ID | Name | File Path | Description |\n")
	_, _ = fmt.Fprintf(&sb, "|----|------|-----------|-------------|\n")
	if len(doc.ObjectTypes) > 0 {
		sort.Slice(doc.ObjectTypes, func(i, j int) bool { return doc.ObjectTypes[i].ID < doc.ObjectTypes[j].ID })
		for _, ot := range doc.ObjectTypes {
			_, _ = fmt.Fprintf(&sb, "| %s | %s | `object_types/%s.bkn` | %s |\n", ot.ID, ot.Name, ot.ID, ot.Summary)
		}
	}

	_, _ = fmt.Fprintf(&sb, "\n### Relation Types\n\n")
	_, _ = fmt.Fprintf(&sb, "| ID | Name | File Path | Description |\n")
	_, _ = fmt.Fprintf(&sb, "|----|------|-----------|-------------|\n")
	if len(doc.RelationTypes) > 0 {
		sort.Slice(doc.RelationTypes, func(i, j int) bool { return doc.RelationTypes[i].ID < doc.RelationTypes[j].ID })
		for _, rt := range doc.RelationTypes {
			fmt.Fprintf(&sb, "| %s | %s | `relation_types/%s.bkn` | %s |\n", rt.ID, rt.Name, rt.ID, rt.Summary)
		}
	}

	_, _ = fmt.Fprintf(&sb, "\n### Action Types\n\n")
	_, _ = fmt.Fprintf(&sb, "| ID | Name | File Path | Description |\n")
	_, _ = fmt.Fprintf(&sb, "|----|------|-----------|-------------|\n")
	if len(doc.ActionTypes) > 0 {
		sort.Slice(doc.ActionTypes, func(i, j int) bool { return doc.ActionTypes[i].ID < doc.ActionTypes[j].ID })
		for _, at := range doc.ActionTypes {
			fmt.Fprintf(&sb, "| %s | %s | `action_types/%s.bkn` | %s |\n", at.ID, at.Name, at.ID, at.Summary)
		}
	}

	_, _ = fmt.Fprintf(&sb, "\n### Risk Types\n\n")
	_, _ = fmt.Fprintf(&sb, "| ID | Name | File Path | Description |\n")
	_, _ = fmt.Fprintf(&sb, "|----|------|-----------|-------------|\n")
	if len(doc.RiskTypes) > 0 {
		sort.Slice(doc.RiskTypes, func(i, j int) bool { return doc.RiskTypes[i].ID < doc.RiskTypes[j].ID })
		for _, rt := range doc.RiskTypes {
			fmt.Fprintf(&sb, "| %s | %s | `risk_types/%s.bkn` | %s |\n", rt.ID, rt.Name, rt.ID, rt.Summary)
		}
	}

	_, _ = fmt.Fprintf(&sb, "\n### Concept Groups\n\n")
	_, _ = fmt.Fprintf(&sb, "| ID | Name | File Path | Description |\n")
	_, _ = fmt.Fprintf(&sb, "|----|------|-----------|-------------|\n")
	if len(doc.ConceptGroups) > 0 {
		sort.Slice(doc.ConceptGroups, func(i, j int) bool { return doc.ConceptGroups[i].ID < doc.ConceptGroups[j].ID })
		for _, cg := range doc.ConceptGroups {
			fmt.Fprintf(&sb, "| %s | %s | `concept_groups/%s.bkn` | %s |\n", cg.ID, cg.Name, cg.ID, cg.Summary)
		}
	}

	sb.WriteString("\n### Metrics\n\n")
	sb.WriteString("| ID | Name | File Path | Description |\n")
	sb.WriteString("|----|------|-----------|-------------|\n")
	if len(doc.Metrics) > 0 {
		sort.Slice(doc.Metrics, func(i, j int) bool { return doc.Metrics[i].ID < doc.Metrics[j].ID })
		for _, met := range doc.Metrics {
			fmt.Fprintf(&sb, "| %s | %s | `metrics/%s.bkn` | %s |\n", met.ID, met.Name, met.ID, met.Summary)
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
	if len(doc.Metrics) > 0 {
		files := make([]string, len(doc.Metrics))
		for i, met := range doc.Metrics {
			files[i] = met.ID + ".bkn"
		}
		dirs = append(dirs, dirEntry{"metrics", files})
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
	var sb strings.Builder
	_, _ = fmt.Fprintf(&sb, "---\n")
	_, _ = fmt.Fprintf(&sb, "type: object_type\n")
	_, _ = fmt.Fprintf(&sb, "id: %s\n", ot.ID)
	_, _ = fmt.Fprintf(&sb, "name: %s\n", ot.Name)
	_, _ = fmt.Fprintf(&sb, "tags: [%s]\n", strings.Join(ot.Tags, ", "))
	_, _ = fmt.Fprintf(&sb, "---\n\n")

	_, _ = fmt.Fprintf(&sb, "## ObjectType: %s\n\n", ot.Name)
	if ot.Description != "" {
		_, _ = fmt.Fprintf(&sb, "%s\n\n", ot.Description)
	}

	// Data Source
	_, _ = fmt.Fprintf(&sb, "### Data Source\n\n")
	_, _ = fmt.Fprintf(&sb, "| Type | ID | Name |\n")
	_, _ = fmt.Fprintf(&sb, "|------|----|------|\n")
	if ot.DataSource != nil {
		_, _ = fmt.Fprintf(&sb, "| %s | %s | %s |\n",
			ot.DataSource.Type, ot.DataSource.ID, ot.DataSource.Name)
	}
	_, _ = fmt.Fprintf(&sb, "\n")

	// Data Properties
	_, _ = fmt.Fprintf(&sb, "### Data Properties\n\n")
	_, _ = fmt.Fprintf(&sb, "| Name | Display Name | Type | Description | Mapped Field |\n")
	_, _ = fmt.Fprintf(&sb, "|------|--------------|------|-------------|--------------|\n")
	if len(ot.DataProperties) > 0 {
		for _, dp := range ot.DataProperties {
			_, _ = fmt.Fprintf(&sb, "| %s | %s | %s | %s | %s |\n",
				dp.Name, dp.DisplayName, dp.Type, dp.Description, dp.MappedField)
		}
	}
	_, _ = fmt.Fprintf(&sb, "\n")

	// Logic Properties
	_, _ = fmt.Fprintf(&sb, "### Logic Properties\n\n")
	for _, lp := range ot.LogicProperties {
		_, _ = fmt.Fprintf(&sb, "#### %s\n\n", lp.Name)

		// Meta table
		_, _ = fmt.Fprintf(&sb, "**Meta**\n\n")
		_, _ = fmt.Fprintf(&sb, "| Display Name | Type | Description |\n")
		_, _ = fmt.Fprintf(&sb, "|--------------|------|-------------|\n")
		_, _ = fmt.Fprintf(&sb, "| %s | %s | %s |\n\n", lp.DisplayName, lp.Type, lp.Description)

		// Source table
		_, _ = fmt.Fprintf(&sb, "**Source**\n\n")
		_, _ = fmt.Fprintf(&sb, "| Source Type | Source ID | Source Name |\n")
		_, _ = fmt.Fprintf(&sb, "|-------------|-----------|-------------|\n")
		if lp.DataSource != nil {
			_, _ = fmt.Fprintf(&sb, "| %s | %s | %s |\n", lp.DataSource.Type, lp.DataSource.ID, lp.DataSource.Name)
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
				p.Name, p.Type, p.Source, p.Operation, p.ValueFrom, v, p.Description)
		}
		_, _ = fmt.Fprintf(&sb, "\n")

		// Analysis Dims table
		_, _ = fmt.Fprintf(&sb, "**Analysis Dimensions**\n\n")
		_, _ = fmt.Fprintf(&sb, "| Name | Display Name | Type | Description |\n")
		_, _ = fmt.Fprintf(&sb, "|------|--------------|------|-------------|\n")
		for _, d := range lp.AnalysisDims {
			_, _ = fmt.Fprintf(&sb, "| %s | %s | %s | %s |\n", d.Name, d.DisplayName, d.Type, d.Description)
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

// SerializeRelationType Serializes BknRelationType to BKN format
func SerializeRelationType(rt *BknRelationType) string {
	var sb strings.Builder
	_, _ = fmt.Fprintf(&sb, "---\n")
	_, _ = fmt.Fprintf(&sb, "type: relation_type\n")
	_, _ = fmt.Fprintf(&sb, "id: %s\n", rt.ID)
	_, _ = fmt.Fprintf(&sb, "name: %s\n", rt.Name)
	_, _ = fmt.Fprintf(&sb, "tags: [%s]\n", strings.Join(rt.Tags, ", "))
	_, _ = fmt.Fprintf(&sb, "---\n\n")

	_, _ = fmt.Fprintf(&sb, "## RelationType: %s\n\n", rt.Name)
	if rt.Description != "" {
		_, _ = fmt.Fprintf(&sb, "%s\n\n", rt.Description)
	}

	// Endpoint
	_, _ = fmt.Fprintf(&sb, "### Endpoint\n\n")
	_, _ = fmt.Fprintf(&sb, "| Source | Target | Type |\n")
	_, _ = fmt.Fprintf(&sb, "|--------|--------|------|\n")
	_, _ = fmt.Fprintf(&sb, "| %s | %s | %s |\n\n", rt.Endpoint.Source, rt.Endpoint.Target, rt.Endpoint.Type)

	switch rt.Endpoint.Type {
	case RELATION_MAPPING_TYPE_DIRECT:
		// ### Mapping Rules — simple source→target property table
		_, _ = fmt.Fprintf(&sb, "### Mapping Rules\n\n")
		_, _ = fmt.Fprintf(&sb, "| Source Property | Target Property |\n")
		_, _ = fmt.Fprintf(&sb, "|-----------------|-----------------|\n")
		if rules, ok := rt.MappingRules.(DirectMappingRule); ok {
			for _, r := range rules {
				_, _ = fmt.Fprintf(&sb, "| %s | %s |\n", r.SourceProperty, r.TargetProperty)
			}
		}
		_, _ = fmt.Fprintf(&sb, "\n")

	case RELATION_MAPPING_TYPE_DATA_VIEW:
		// ### Mapping View — backing data source reference
		_, _ = fmt.Fprintf(&sb, "### Mapping View\n\n")
		_, _ = fmt.Fprintf(&sb, "| Type | ID |\n")
		_, _ = fmt.Fprintf(&sb, "|------|----|\n")
		if rules, ok := rt.MappingRules.(*InDirectMappingRule); ok {
			if rules.BackingDataSource != nil {
				_, _ = fmt.Fprintf(&sb, "| %s | %s |\n", rules.BackingDataSource.Type, rules.BackingDataSource.ID)
			}
			_, _ = fmt.Fprintf(&sb, "\n")

			// ### Source Mapping — source property → view property
			_, _ = fmt.Fprintf(&sb, "### Source Mapping\n\n")
			_, _ = fmt.Fprintf(&sb, "| Source Property | View Property |\n")
			_, _ = fmt.Fprintf(&sb, "|-----------------|---------------|\n")
			for _, r := range rules.SourceMappingRules {
				_, _ = fmt.Fprintf(&sb, "| %s | %s |\n", r.SourceProperty, r.TargetProperty)
			}
			_, _ = fmt.Fprintf(&sb, "\n")

			// ### Target Mapping — view property → target property
			_, _ = fmt.Fprintf(&sb, "### Target Mapping\n\n")
			_, _ = fmt.Fprintf(&sb, "| View Property | Target Property |\n")
			_, _ = fmt.Fprintf(&sb, "|---------------|-----------------|\n")
			for _, r := range rules.TargetMappingRules {
				_, _ = fmt.Fprintf(&sb, "| %s | %s |\n", r.SourceProperty, r.TargetProperty)
			}
			_, _ = fmt.Fprintf(&sb, "\n")
		} else {
			_, _ = fmt.Fprintf(&sb, "\n")
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
	var sb strings.Builder
	intent := strings.TrimSpace(at.ActionIntent)
	actionType := strings.TrimSpace(at.ActionType)
	if intent == "" && actionType != "" {
		intent = actionType
	}

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

	_, _ = fmt.Fprintf(&sb, "## ActionType: %s\n\n", at.Name)
	if at.Description != "" {
		_, _ = fmt.Fprintf(&sb, "%s\n\n", at.Description)
	}

	// Bound Object
	_, _ = fmt.Fprintf(&sb, "### Bound Object\n\n")
	_, _ = fmt.Fprintf(&sb, "| Bound Object |\n")
	_, _ = fmt.Fprintf(&sb, "|--------------|\n")
	if at.BoundObject != "" {
		_, _ = fmt.Fprintf(&sb, "| %s |\n", at.BoundObject)
	}
	_, _ = fmt.Fprintf(&sb, "\n")

	// Affect Object
	_, _ = fmt.Fprintf(&sb, "### Affect Object\n\n")
	_, _ = fmt.Fprintf(&sb, "| Affect Object | Affect Description |\n")
	_, _ = fmt.Fprintf(&sb, "|---------------|--------------------|\n")
	if at.AffectObject != nil {
		_, _ = fmt.Fprintf(&sb, "| %s | %s |\n", at.AffectObject.ObjectType, at.AffectObject.Description)
	}
	_, _ = fmt.Fprintf(&sb, "\n")

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
			at.ActionSource.Type, at.ActionSource.BoxID, at.ActionSource.ToolID,
			at.ActionSource.McpID, at.ActionSource.ToolName)
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
			p.Name, p.Type, p.Source, p.Operation, p.ValueFrom, v, p.Description)
	}
	_, _ = fmt.Fprintf(&sb, "\n")

	// Schedule
	_, _ = fmt.Fprintf(&sb, "### Schedule\n\n")
	_, _ = fmt.Fprintf(&sb, "| Type | Expression |\n")
	_, _ = fmt.Fprintf(&sb, "|------|------------|\n")
	if at.Schedule != nil && at.Schedule.Type != "" {
		_, _ = fmt.Fprintf(&sb, "| %s | %s |\n", at.Schedule.Type, at.Schedule.Expression)
	}
	_, _ = fmt.Fprintf(&sb, "\n")

	return sb.String()
}

// SerializeRiskType Serializes BknRiskType to BKN format
func SerializeRiskType(rt *BknRiskType) string {
	var sb strings.Builder
	_, _ = fmt.Fprintf(&sb, "---\n")
	_, _ = fmt.Fprintf(&sb, "type: risk_type\n")
	_, _ = fmt.Fprintf(&sb, "id: %s\n", rt.ID)
	_, _ = fmt.Fprintf(&sb, "name: %s\n", rt.Name)
	_, _ = fmt.Fprintf(&sb, "tags: [%s]\n", strings.Join(rt.Tags, ", "))
	_, _ = fmt.Fprintf(&sb, "---\n\n")

	_, _ = fmt.Fprintf(&sb, "## RiskType: %s\n\n", rt.Name)
	if rt.Description != "" {
		_, _ = fmt.Fprintf(&sb, "%s\n\n", rt.Description)
	}

	return sb.String()
}

// SerializeConceptGroup Serializes BknConceptGroup to BKN format.
// otIndex maps ObjectType ID → *BknObjectType for Name/Description lookup.
func SerializeConceptGroup(cg *BknConceptGroup, otIndex map[string]*BknObjectType) string {
	var sb strings.Builder
	_, _ = fmt.Fprintf(&sb, "---\n")
	_, _ = fmt.Fprintf(&sb, "type: concept_group\n")
	_, _ = fmt.Fprintf(&sb, "id: %s\n", cg.ID)
	_, _ = fmt.Fprintf(&sb, "name: %s\n", cg.Name)
	_, _ = fmt.Fprintf(&sb, "tags: [%s]\n", strings.Join(cg.Tags, ", "))
	_, _ = fmt.Fprintf(&sb, "---\n\n")

	_, _ = fmt.Fprintf(&sb, "## ConceptGroup: %s\n\n", cg.Name)
	if cg.Description != "" {
		_, _ = fmt.Fprintf(&sb, "%s\n\n", cg.Description)
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
			_, _ = fmt.Fprintf(&sb, "| %s | %s | %s |\n", id, name, desc)
		}
	}
	_, _ = fmt.Fprintf(&sb, "\n")

	return sb.String()
}
