// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package common

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// FrontmatterSetter replaces the value of one key in a frontmatter being edited.
type FrontmatterSetter func(key *yaml.Node, value *yaml.Node, replacement string) error

// EditFrontmatter parses the YAML frontmatter of a file and lets edit replace scalar
// values through set, keeping every other byte of the file. Serializing the parsed
// YAML instead would rebuild the whole frontmatter, comments and key order included.
// Only a value written on its key's own line can be replaced; set refuses any other.
func EditFrontmatter(text string, fileName string,
	edit func(root *yaml.Node, set FrontmatterSetter) error) (string, error) {
	const delimiter = "---"
	bom := ""
	if strings.HasPrefix(text, "\ufeff") {
		bom, text = "\ufeff", strings.TrimPrefix(text, "\ufeff")
	}
	open := strings.Index(text, "\n")
	if !strings.HasPrefix(text, delimiter) || open < 0 {
		return "", fmt.Errorf("%s has no frontmatter", fileName)
	}
	end := strings.Index(text[open:], "\n"+delimiter)
	if end < 0 {
		return "", fmt.Errorf("%s has no frontmatter end", fileName)
	}
	frontmatter := text[open+1 : open+end+1]

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(frontmatter), &doc); err != nil {
		return "", fmt.Errorf("parse %s frontmatter: %w", fileName, err)
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return "", fmt.Errorf("%s frontmatter is not a mapping", fileName)
	}

	lines := strings.Split(frontmatter, "\n")
	set := func(key *yaml.Node, value *yaml.Node, replacement string) error {
		if value.Kind != yaml.ScalarNode || value.Line != key.Line ||
			value.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
			return fmt.Errorf("%s frontmatter %s is not a one-line value", fileName, key.Value)
		}
		var encoded yaml.Node
		if err := encoded.Encode(replacement); err != nil {
			return err
		}
		out, err := yaml.Marshal(&encoded)
		if err != nil {
			return err
		}
		line := lines[value.Line-1]
		cr := strings.HasSuffix(line, "\r")
		// Everything from the value to the end of the line is the value, and a
		// trailing comment if there was one.
		line = line[:value.Column-1] + strings.TrimSuffix(string(out), "\n")
		if cr {
			line += "\r"
		}
		lines[value.Line-1] = line
		return nil
	}
	if err := edit(doc.Content[0], set); err != nil {
		return "", err
	}
	return bom + text[:open+1] + strings.Join(lines, "\n") + text[open+end+1:], nil
}

// MappingValue returns the value of key in a YAML mapping node, or nil.
func MappingValue(mapping *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i], mapping.Content[i+1]
		}
	}
	return nil, nil
}
