// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// serializeOptions selects the specification a package is written in. The zero value writes 2.x,
// byte for byte as before bkn_spec existed; v3 writes the one spelling 3.0 accepts
// (SPECIFICATION.md §1.3, §14.2).
type serializeOptions struct {
	v3 bool
}

// description returns a description as written into the file: under 3.0 a line that would read as
// a heading is escaped, so the import reads it back unchanged.
func (o serializeOptions) description(desc string) string {
	if o.v3 {
		return escapeDescriptionV3(desc)
	}
	return desc
}

// frontmatterField is one frontmatter field in the order it is written.
type frontmatterField struct {
	key   string
	value *yaml.Node
}

func scalarField(key, value string) frontmatterField {
	return frontmatterField{key: key, value: strNode(value)}
}

// listField writes a list in flow style, as [a, b].
func listField(key string, items []string) frontmatterField {
	node := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle}
	for _, item := range items {
		node.Content = append(node.Content, strNode(item))
	}
	return frontmatterField{key: key, value: node}
}

func capabilitiesField(capabilities *BknCapabilities) *frontmatterField {
	if capabilities == nil || (len(capabilities.Skills) == 0 && len(capabilities.Functions) == 0 &&
		len(capabilities.MCPTools) == 0) {
		return nil
	}
	var node yaml.Node
	if err := node.Encode(capabilities); err != nil {
		return nil
	}
	return &frontmatterField{key: "capabilities", value: &node}
}

// strNode is a string scalar. The encoder quotes it whenever the plain form would read back as
// something else, so 001, 1.0 and "订单: 明细" survive the round trip.
func strNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

// writeFrontmatterV3 writes frontmatter through the YAML encoder instead of string concatenation,
// which is what keeps every string field a string (appendix A-3).
func writeFrontmatterV3(sb *strings.Builder, fields []frontmatterField) {
	root := &yaml.Node{Kind: yaml.MappingNode}
	for _, f := range fields {
		root.Content = append(root.Content, strNode(f.key), f.value)
	}
	sb.WriteString("---\n")
	sb.WriteString(encodeYAML(root))
	sb.WriteString("---\n\n")
}

// writeDefinitionFrontmatter writes the type, id, name and tags every definition file starts with.
// Under 3.0 the extra fields follow; 2.x has no place for them.
func writeDefinitionFrontmatter(sb *strings.Builder, opts serializeOptions, typ, id, name string, tags []string, extra ...frontmatterField) {
	if opts.v3 {
		fields := []frontmatterField{
			scalarField("type", typ), scalarField("id", id), scalarField("name", name), listField("tags", tags),
		}
		writeFrontmatterV3(sb, append(fields, extra...))
		return
	}
	_, _ = fmt.Fprintf(sb, "---\n")
	_, _ = fmt.Fprintf(sb, "type: %s\n", typ)
	_, _ = fmt.Fprintf(sb, "id: %s\n", id)
	_, _ = fmt.Fprintf(sb, "name: %s\n", name)
	_, _ = fmt.Fprintf(sb, "tags: [%s]\n", strings.Join(tags, ", "))
	_, _ = fmt.Fprintf(sb, "---\n\n")
}

func encodeYAML(node *yaml.Node) string {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	_ = enc.Encode(node)
	_ = enc.Close()
	return buf.String()
}

// aliasesField is the aliases frontmatter field, or nothing when there are none.
func aliasesField(aliases []string) []frontmatterField {
	if len(aliases) == 0 {
		return nil
	}
	return []frontmatterField{listField("aliases", aliases)}
}

// lifecycleField is the lifecycle frontmatter field, or nothing for an active definition.
func lifecycleField(lc *Lifecycle) []frontmatterField {
	if lc == nil || lc.Status == LifecycleActive {
		return nil
	}
	node := &yaml.Node{Kind: yaml.MappingNode}
	node.Content = append(node.Content, strNode("status"), strNode(lc.Status))
	if lc.Reason != "" {
		node.Content = append(node.Content, strNode("reason"), strNode(lc.Reason))
	}
	if lc.ReplacedBy != "" {
		node.Content = append(node.Content, strNode("replaced_by"), strNode(lc.ReplacedBy))
	}
	return []frontmatterField{{key: "lifecycle", value: node}}
}

// relationFields are the 3.0 frontmatter fields of a relation type after tags, each written only
// when declared; importance only when it is secondary.
func relationFields(rt *BknRelationType) []frontmatterField {
	importance := rt.Importance
	if importance == ImportancePrimary {
		importance = ""
	}
	fields := aliasesField(rt.Aliases)
	for _, f := range []struct{ key, value string }{
		{"reverse_name", rt.ReverseName},
		{"importance", importance},
		{"fallback_for", rt.FallbackFor},
		{"verification", rt.Verification},
	} {
		if f.value != "" {
			fields = append(fields, scalarField(f.key, f.value))
		}
	}
	return append(fields, lifecycleField(rt.Lifecycle)...)
}

// writeRelationConstraintsV3 writes ### Relation Constraints when the relation declares any.
func writeRelationConstraintsV3(sb *strings.Builder, rt *BknRelationType) {
	if len(rt.RequiredEnds) == 0 && len(rt.Characteristics) == 0 {
		return
	}
	root := &yaml.Node{Kind: yaml.MappingNode}
	if len(rt.RequiredEnds) > 0 {
		root.Content = append(root.Content, strNode("required"), listField("", rt.RequiredEnds).value)
	}
	if len(rt.Characteristics) > 0 {
		root.Content = append(root.Content, strNode("characteristics"), listField("", rt.Characteristics).value)
	}
	_, _ = fmt.Fprintf(sb, "### Relation Constraints\n\n%s\n", encodeYAMLBlock(root))
}

// writeDataPropertiesV3 writes the 3.0 Data Properties table and, when a property has constraints,
// the ### Property Constraints section after it. The columns are in a fixed order; Semantic,
// Required, Aliases and Mask Rule are written only when a property uses them, and Required stays
// empty for a primary key, which is required by definition. A relation has no primary keys.
func writeDataPropertiesV3(sb *strings.Builder, props []*DataProperty, primaryKeys []string) {
	var hasSemantic, hasRequired, hasAliases, hasMaskRule, hasConstraints bool
	for _, dp := range props {
		hasSemantic = hasSemantic || dp.Semantic != ""
		hasRequired = hasRequired || requiredCell(dp, primaryKeys) != ""
		hasAliases = hasAliases || len(dp.Aliases) > 0
		hasMaskRule = hasMaskRule || dp.MaskRule != nil
		hasConstraints = hasConstraints || dp.Constraints != nil
	}
	type column struct {
		title string
		cell  func(*DataProperty) string
	}
	columns := []column{
		{"Name", func(dp *DataProperty) string { return dp.Name }},
		{"Display Name", func(dp *DataProperty) string { return dp.DisplayName }},
		{"Type", func(dp *DataProperty) string { return dp.Type }},
	}
	if hasSemantic {
		columns = append(columns, column{"Semantic", func(dp *DataProperty) string { return dp.Semantic }})
	}
	if hasRequired {
		columns = append(columns, column{"Required", func(dp *DataProperty) string { return requiredCell(dp, primaryKeys) }})
	}
	if hasAliases {
		columns = append(columns, column{"Aliases", func(dp *DataProperty) string { return strings.Join(dp.Aliases, aliasSeparator+" ") }})
	}
	columns = append(columns,
		column{"Description", func(dp *DataProperty) string { return dp.Description }},
		column{"Mapped Field", func(dp *DataProperty) string { return dp.MappedField }},
	)
	if hasMaskRule {
		columns = append(columns, column{"Mask Rule", func(dp *DataProperty) string { return serializeMaskRule(dp.MaskRule) }})
	}

	titles := make([]string, len(columns))
	rules := make([]string, len(columns))
	for i, c := range columns {
		titles[i] = c.title
		rules[i] = strings.Repeat("-", len(c.title)+2)
	}
	_, _ = fmt.Fprintf(sb, "| %s |\n", strings.Join(titles, " | "))
	_, _ = fmt.Fprintf(sb, "|%s|\n", strings.Join(rules, "|"))
	for _, dp := range props {
		cells := make([]string, len(columns))
		for i, c := range columns {
			cells[i] = tableCell(c.cell(dp))
		}
		_, _ = fmt.Fprintf(sb, "| %s |\n", strings.Join(cells, " | "))
	}
	_, _ = fmt.Fprintf(sb, "\n")

	if hasConstraints {
		_, _ = fmt.Fprintf(sb, "### Property Constraints\n\n")
		_, _ = fmt.Fprintf(sb, "%s\n", encodeYAMLBlock(propertyConstraintsNode(props)))
	}
}

func requiredCell(dp *DataProperty, primaryKeys []string) string {
	if dp.Required && !containsString(primaryKeys, dp.Name) {
		return "true"
	}
	return ""
}

// propertyConstraintsNode lists the constraints by property in table order, each property's keys
// in the order of SPECIFICATION.md §6.8.
func propertyConstraintsNode(props []*DataProperty) *yaml.Node {
	root := &yaml.Node{Kind: yaml.MappingNode}
	for _, dp := range props {
		c := dp.Constraints
		if c == nil {
			continue
		}
		entry := &yaml.Node{Kind: yaml.MappingNode}
		add := func(key string, value *yaml.Node) {
			entry.Content = append(entry.Content, strNode(key), value)
		}
		if c.ValueType != "" {
			add("value_type", strNode(c.ValueType))
		}
		addValueRules(add, c.Range, c.Length, c.Pattern, c.Unit)
		if c.Unique {
			add("unique", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"})
		}
		if c.CRS != "" {
			add("crs", strNode(c.CRS))
		}
		if c.Timezone != "" {
			add("timezone", strNode(c.Timezone))
		}
		root.Content = append(root.Content, strNode(dp.Name), entry)
	}
	return root
}

// addValueRules adds range, length, pattern and unit, in that order, leaving out what is not
// declared.
func addValueRules(add func(string, *yaml.Node), rng *ValueRange, length *LengthRange, pattern, unit string) {
	if rng != nil {
		add("range", boundsNode(rng.Min, rng.Max))
	}
	if length != nil {
		var lo, hi any
		if length.Min != nil {
			lo = int64(*length.Min)
		}
		if length.Max != nil {
			hi = int64(*length.Max)
		}
		add("length", boundsNode(lo, hi))
	}
	if pattern != "" {
		add("pattern", strNode(pattern))
	}
	if unit != "" {
		add("unit", strNode(unit))
	}
}

// boundsNode writes {min: x, max: y}, leaving out an absent bound.
func boundsNode(lo, hi any) *yaml.Node {
	node := &yaml.Node{Kind: yaml.MappingNode, Style: yaml.FlowStyle}
	for _, b := range []struct {
		key   string
		value any
	}{{"min", lo}, {"max", hi}} {
		if b.value == nil {
			continue
		}
		node.Content = append(node.Content, strNode(b.key), boundNode(b.value))
	}
	return node
}

func boundNode(v any) *yaml.Node {
	switch n := v.(type) {
	case int64:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.FormatInt(n, 10)}
	case float64:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!float", Value: strconv.FormatFloat(n, 'g', -1, 64)}
	}
	return strNode(fmt.Sprint(v))
}

// serializeValueType writes a value type file. Value types exist only in 3.0.
func serializeValueType(vt *BknValueType) string {
	var sb strings.Builder
	opts := serializeOptions{v3: true}
	writeDefinitionFrontmatter(&sb, opts, kindValueType, vt.ID, vt.Name, vt.Tags)
	_, _ = fmt.Fprintf(&sb, "## ValueType: %s\n\n", vt.Name)
	if vt.Description != "" {
		_, _ = fmt.Fprintf(&sb, "%s\n\n", opts.description(vt.Description))
	}

	root := &yaml.Node{Kind: yaml.MappingNode}
	add := func(key string, value *yaml.Node) {
		root.Content = append(root.Content, strNode(key), value)
	}
	add("base_type", strNode(vt.BaseType))
	if vt.Enum != nil {
		enum := &yaml.Node{Kind: yaml.SequenceNode}
		for _, item := range vt.Enum {
			entry := &yaml.Node{Kind: yaml.MappingNode, Style: yaml.FlowStyle}
			entry.Content = append(entry.Content, strNode("value"), enumValueNode(vt.BaseType, item.Value),
				strNode("display"), strNode(item.Display))
			if item.Deprecated {
				entry.Content = append(entry.Content, strNode("deprecated"), &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"})
			}
			if item.Description != "" {
				entry.Content = append(entry.Content, strNode("description"), strNode(item.Description))
			}
			enum.Content = append(enum.Content, entry)
		}
		add("enum", enum)
	}
	addValueRules(add, vt.Range, vt.Length, vt.Pattern, vt.Unit)
	_, _ = fmt.Fprintf(&sb, "### Definition\n\n%s", encodeYAMLBlock(root))
	return sb.String()
}

// enumValueNode writes an enum value as a number for a numeric base type and as a string otherwise,
// so that a code such as "0101" keeps its leading zero.
func enumValueNode(baseType, value string) *yaml.Node {
	switch baseType {
	case "integer", "unsigned integer":
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: value}
	case "float", "decimal":
		if _, err := strconv.ParseInt(value, 10, 64); err == nil {
			return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: value}
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!float", Value: value}
	}
	return strNode(value)
}
