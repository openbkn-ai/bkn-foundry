// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// A value type (SPECIFICATION.md §16) is a base type with a set of value rules, named so that
// several properties can share it: Type says how the data is stored, the value type says what it
// is and which values are valid. Value types belong to one network and exist only in 3.0; the 2.x
// reader never looks into value_types/.

// valueBaseTypes are the types a value type may be based on.
var valueBaseTypes = []string{"string", "keyword", "integer", "unsigned integer", "float", "decimal", "date", "datetime", "timestamp"}

// definitionKeys is the order the keys of ### Definition are written in.
var definitionKeys = []string{"base_type", "enum", "range", "length", "pattern", "unit"}

// enumItemKeys is the order the keys of one enum item are written in.
var enumItemKeys = []string{"value", "display", "deprecated", "description"}

// parseValueTypeFile parses a value_types/*.bkn file. Value types exist only in 3.0, so the file is
// always read by the 3.0 rules.
func parseValueTypeFile(text string) (*BknValueType, error) {
	raw, err := parseRawFrontmatter(text)
	if err != nil {
		return nil, err
	}
	if err := checkDefinitionV3(kindValueType, text, raw); err != nil {
		return nil, err
	}
	desc, sections, _ := extractSectionsWithDesc(text)
	vt := &BknValueType{
		BknValueTypeFrontmatter: BknValueTypeFrontmatter{
			Type: kindValueType,
			ID:   raw.str("id"),
			Name: raw.str("name"),
			Tags: raw.list("tags"),
		},
		Description: unescapeDescriptionV3(desc),
		RawContent:  text,
	}
	vt.Summary = ExtractSummary(vt.Description)
	if err := parseValueTypeDefinition(vt, sections["Definition"]); err != nil {
		return nil, err
	}
	if err := ValidateValueType(vt); err != nil {
		return nil, err
	}
	return vt, nil
}

// parseValueTypeDefinition reads the yaml block of ### Definition.
func parseValueTypeDefinition(vt *BknValueType, sectionText string) error {
	content := scanContent(sectionText)
	if len(content.blocks) == 0 {
		return specErrorf("value type %q needs a ### Definition section with a yaml block that names its base_type", vt.ID)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(content.blocks[0].body), &doc); err != nil {
		return specErrorf("section \"### Definition\" yaml is invalid: %v", err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return specErrorf("section \"### Definition\" must be a mapping with base_type")
	}
	root := doc.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		key, value := root.Content[i].Value, root.Content[i+1]
		where := fmt.Sprintf("value type %q %s", vt.ID, key)
		switch key {
		case "base_type", "pattern", "unit":
			if value.Kind != yaml.ScalarNode || value.Tag != "!!str" || value.Value == "" {
				return specErrorf("%s must be a non-empty string", where)
			}
			switch key {
			case "base_type":
				vt.BaseType = value.Value
			case "pattern":
				vt.Pattern = value.Value
			case "unit":
				vt.Unit = value.Value
			}
		case "enum":
			items, err := parseEnumNode(where, value)
			if err != nil {
				return err
			}
			vt.Enum = items
		case "range":
			r, err := parseRangeNode(where, value)
			if err != nil {
				return err
			}
			vt.Range = r
		case "length":
			l, err := parseLengthNode(where, value)
			if err != nil {
				return err
			}
			vt.Length = l
		default:
			return specErrorf("%s is not a value type key; the keys are %s", where, strings.Join(definitionKeys, ", "))
		}
	}
	return nil
}

func parseEnumNode(where string, node *yaml.Node) ([]*ValueTypeEnumItem, error) {
	if node.Kind != yaml.SequenceNode {
		return nil, specErrorf("%s must be a list of items such as - { value: N, display: Open }", where)
	}
	items := make([]*ValueTypeEnumItem, 0, len(node.Content))
	for n, itemNode := range node.Content {
		at := fmt.Sprintf("%s item %d", where, n+1)
		if itemNode.Kind != yaml.MappingNode {
			return nil, specErrorf("%s must be a mapping with value and display", at)
		}
		item := &ValueTypeEnumItem{}
		for i := 0; i+1 < len(itemNode.Content); i += 2 {
			key, value := itemNode.Content[i].Value, itemNode.Content[i+1]
			switch key {
			case "value":
				if value.Kind != yaml.ScalarNode || value.Tag == "!!null" {
					return nil, specErrorf("%s value must be a single value", at)
				}
				item.Value = value.Value
			case "display", "description":
				if value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
					return nil, specErrorf("%s %s must be a string", at, key)
				}
				if key == "display" {
					item.Display = value.Value
				} else {
					item.Description = value.Value
				}
			case "deprecated":
				if value.Kind != yaml.ScalarNode || value.Tag != "!!bool" {
					return nil, specErrorf("%s deprecated must be true or false", at)
				}
				item.Deprecated = value.Value == "true"
			default:
				return nil, specErrorf("%s key %q is unknown; the keys are %s", at, key, strings.Join(enumItemKeys, ", "))
			}
		}
		items = append(items, item)
	}
	return items, nil
}

// ValidateValueType checks a value type on its own: its base_type, its enum and its value rules.
// Import runs it on every value type file; a save through the API runs the same check.
func ValidateValueType(vt *BknValueType) error {
	where := fmt.Sprintf("value type %q", vt.ID)
	if !containsString(valueBaseTypes, vt.BaseType) {
		return specErrorf("%s: base_type must be one of %s, got %q", where, strings.Join(valueBaseTypes, ", "), vt.BaseType)
	}
	if vt.Enum != nil {
		if vt.Range != nil || vt.Length != nil || vt.Pattern != "" {
			return specErrorf("%s: an enum lists the valid values, so range, length and pattern do not go with it", where)
		}
		if len(vt.Enum) == 0 {
			return specErrorf("%s: enum lists no items", where)
		}
		seen := map[string]int{}
		for n, item := range vt.Enum {
			at := fmt.Sprintf("%s enum item %d", where, n+1)
			if item.Value == "" {
				return specErrorf("%s needs a value", at)
			}
			if item.Display == "" {
				return specErrorf("%s needs a display", at)
			}
			key, ok := enumValueKey(vt.BaseType, item.Value)
			if !ok {
				return specErrorf("%s: value %q does not fit base_type %s", at, item.Value, vt.BaseType)
			}
			if first, dup := seen[key]; dup {
				return specErrorf("%s: value %q is the same as item %d; enum values do not repeat", at, item.Value, first)
			}
			seen[key] = n + 1
		}
	}
	return validateValueRules(where, vt.BaseType, vt.BaseType, vt.Range, vt.Length, vt.Pattern, vt.Unit)
}

// enumValueKey reports whether an enum value fits the base type, and the key two values that mean
// the same compare equal by: 1 and 1.0 of a decimal, two spellings of the same instant.
func enumValueKey(baseType, value string) (string, bool) {
	switch baseType {
	case "integer":
		n, err := strconv.ParseInt(value, 10, 64)
		return strconv.FormatInt(n, 10), err == nil
	case "unsigned integer":
		n, err := strconv.ParseUint(value, 10, 64)
		return strconv.FormatUint(n, 10), err == nil
	case "float", "decimal":
		f, err := strconv.ParseFloat(value, 64)
		return strconv.FormatFloat(f, 'g', -1, 64), err == nil
	case "date", "datetime", "timestamp":
		t, ok := timeBound(baseType, value)
		return t.UTC().String(), ok
	}
	return value, true
}
