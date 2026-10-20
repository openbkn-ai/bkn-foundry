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
func writeDefinitionFrontmatter(sb *strings.Builder, opts serializeOptions, typ, id, name string, tags []string) {
	if opts.v3 {
		writeFrontmatterV3(sb, []frontmatterField{
			scalarField("type", typ), scalarField("id", id), scalarField("name", name), listField("tags", tags),
		})
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

// writeDataPropertiesV3 writes the 3.0 Data Properties table and, when a property has constraints,
// the ### Property Constraints section after it. The columns are in a fixed order; Semantic,
// Required and Mask Rule are written only when a property uses them, and Required stays empty for
// a primary key, which is required by definition.
func writeDataPropertiesV3(sb *strings.Builder, ot *BknObjectType) {
	var hasSemantic, hasRequired, hasMaskRule, hasConstraints bool
	for _, dp := range ot.DataProperties {
		hasSemantic = hasSemantic || dp.Semantic != ""
		hasRequired = hasRequired || requiredCell(dp, ot.PrimaryKeys) != ""
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
		columns = append(columns, column{"Required", func(dp *DataProperty) string { return requiredCell(dp, ot.PrimaryKeys) }})
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
	for _, dp := range ot.DataProperties {
		cells := make([]string, len(columns))
		for i, c := range columns {
			cells[i] = tableCell(c.cell(dp))
		}
		_, _ = fmt.Fprintf(sb, "| %s |\n", strings.Join(cells, " | "))
	}
	_, _ = fmt.Fprintf(sb, "\n")

	if hasConstraints {
		_, _ = fmt.Fprintf(sb, "### Property Constraints\n\n")
		_, _ = fmt.Fprintf(sb, "%s\n", encodeYAMLBlock(propertyConstraintsNode(ot.DataProperties)))
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
		if c.Range != nil {
			add("range", boundsNode(c.Range.Min, c.Range.Max))
		}
		if c.Length != nil {
			var lo, hi any
			if c.Length.Min != nil {
				lo = int64(*c.Length.Min)
			}
			if c.Length.Max != nil {
				hi = int64(*c.Length.Max)
			}
			add("length", boundsNode(lo, hi))
		}
		if c.Pattern != "" {
			add("pattern", strNode(c.Pattern))
		}
		if c.Unit != "" {
			add("unit", strNode(c.Unit))
		}
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
