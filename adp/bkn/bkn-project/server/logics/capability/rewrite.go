// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package capability

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/bkn-specification/bkn"
	"gopkg.in/yaml.v3"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
)

// separatorCell matches the cells of the line under a table header.
var separatorCell = regexp.MustCompile(`^:?-+:?$`)

// RewritePackage points the definitions of a new network at the copies made
// for it and at the network itself, keeping every other byte:
//   - in every table of a .bkn file, a BoxID or ToolID cell of a copied
//     toolbox or tool (action sources and logic property sources), and a
//     const parameter whose value is oldNetworkID;
//   - in the capabilities of network.bkn, the box_id, tool_id and box_name of
//     a copied toolbox and the id and name of a copied Skill.
func RewritePackage(pkg map[string][]byte, oldNetworkID string, newNetworkID string,
	copies []*Copy) (map[string][]byte, error) {
	boxes, tools, skills := map[string]*Copy{}, map[string]string{}, map[string]*Copy{}
	for _, c := range copies {
		if c.Kind == KIND_FUNCTION {
			boxes[c.OldID] = c
			for oldID, newID := range c.ToolIDs {
				tools[oldID] = newID
			}
		} else {
			skills[c.OldID] = c
		}
	}

	out := make(map[string][]byte, len(pkg))
	for p, data := range pkg {
		if strings.HasSuffix(p, ".bkn") {
			data = []byte(rewriteTables(string(data), func(column string, row map[string]string, value string) string {
				switch column {
				case "boxid", "toolboxid":
					if c, ok := boxes[value]; ok {
						return c.NewID
					}
				case "toolid":
					if id, ok := tools[value]; ok {
						return id
					}
				case "value":
					if value == oldNetworkID && row["valuefrom"] == "const" {
						return newNetworkID
					}
				}
				return value
			}))
		}
		out[p] = data
	}
	if len(copies) == 0 {
		return out, nil
	}
	text, err := rewriteCapabilities(string(out[bkn.RootFileName]), boxes, tools, skills)
	if err != nil {
		return nil, err
	}
	out[bkn.RootFileName] = []byte(text)
	return out, nil
}

// rewriteCapabilities edits the capabilities section of network.bkn in place.
func rewriteCapabilities(text string, boxes map[string]*Copy, tools map[string]string,
	skills map[string]*Copy) (string, error) {
	return common.EditFrontmatter(text, bkn.RootFileName, func(root *yaml.Node, set common.FrontmatterSetter) error {
		_, capabilities := common.MappingValue(root, "capabilities")
		// rewrites lists, per entry, the field whose value moves and its new value.
		rewrite := func(category string, fields func(entry *yaml.Node) map[string]string) error {
			_, list := common.MappingValue(capabilities, category)
			if list == nil || list.Kind != yaml.SequenceNode {
				return nil
			}
			for _, entry := range list.Content {
				changes := fields(entry)
				if len(changes) == 0 {
					continue
				}
				// A value is replaced up to the end of its line, which a flow
				// mapping shares with the rest of the entry.
				if entry.Style&yaml.FlowStyle != 0 || list.Style&yaml.FlowStyle != 0 || capabilities.Style&yaml.FlowStyle != 0 {
					return fmt.Errorf("%s capabilities %s are written in flow style and cannot be rewritten", bkn.RootFileName, category)
				}
				for key, value := range changes {
					k, v := common.MappingValue(entry, key)
					if err := set(k, v, value); err != nil {
						return err
					}
				}
			}
			return nil
		}
		field := func(entry *yaml.Node, key string) (string, bool) {
			_, v := common.MappingValue(entry, key)
			if v == nil {
				return "", false
			}
			return v.Value, true
		}
		err := rewrite("functions", func(entry *yaml.Node) map[string]string {
			changes := map[string]string{}
			boxID, _ := field(entry, "box_id")
			c, ok := boxes[boxID]
			if !ok {
				return changes
			}
			changes["box_id"] = c.NewID
			if _, ok := field(entry, "box_name"); ok {
				changes["box_name"] = c.NewName
			}
			if toolID, ok := field(entry, "tool_id"); ok {
				if newID, ok := tools[toolID]; ok {
					changes["tool_id"] = newID
				}
			}
			return changes
		})
		if err != nil {
			return err
		}
		return rewrite("skills", func(entry *yaml.Node) map[string]string {
			changes := map[string]string{}
			id, _ := field(entry, "id")
			c, ok := skills[id]
			if !ok {
				return changes
			}
			changes["id"] = c.NewID
			if _, ok := field(entry, "name"); ok {
				changes["name"] = c.NewName
			}
			return changes
		})
	})
}

// rewriteTables runs replace over every data cell of every Markdown table in
// text and writes back the cells it changes, keeping the rest of the line.
// replace gets the column name in lower case without spaces, the row keyed
// the same way, and the cell value.
func rewriteTables(text string, replace func(column string, row map[string]string, value string) string) string {
	lines := strings.Split(text, "\n")
	var header []string
	for i, line := range lines {
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			header = nil
			continue
		}
		cells := tableCells(line)
		values := make([]string, len(cells))
		for n, c := range cells {
			values[n] = line[c[0]:c[1]]
		}
		if header == nil {
			header = make([]string, len(values))
			for n, v := range values {
				header[n] = strings.ToLower(strings.ReplaceAll(v, " ", ""))
			}
			continue
		}
		if isSeparator(values) {
			continue
		}
		row := map[string]string{}
		for n, v := range values {
			if n < len(header) {
				row[header[n]] = v
			}
		}
		// Replace from the last cell back, so earlier offsets stay valid.
		for n := len(cells) - 1; n >= 0; n-- {
			if n >= len(header) {
				continue
			}
			if next := replace(header[n], row, values[n]); next != values[n] {
				line = line[:cells[n][0]] + next + line[cells[n][1]:]
			}
		}
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}

// tableCells returns the start and end of the trimmed value of each cell of a
// table row. A pipe escaped as \| stays inside its cell.
func tableCells(line string) [][2]int {
	var pipes []int
	for i := 0; i < len(line); i++ {
		switch {
		case line[i] == '\\' && i+1 < len(line) && line[i+1] == '|':
			i++
		case line[i] == '|':
			pipes = append(pipes, i)
		}
	}
	// A row that does not end in a pipe still has a last cell.
	if rest := strings.TrimSpace(line[pipes[len(pipes)-1]+1:]); rest != "" {
		pipes = append(pipes, len(line))
	}
	cells := make([][2]int, 0, len(pipes))
	for n := 0; n+1 < len(pipes); n++ {
		start, end := pipes[n]+1, pipes[n+1]
		for start < end && (line[start] == ' ' || line[start] == '\t') {
			start++
		}
		for end > start && (line[end-1] == ' ' || line[end-1] == '\t' || line[end-1] == '\r') {
			end--
		}
		cells = append(cells, [2]int{start, end})
	}
	return cells
}

func isSeparator(values []string) bool {
	for _, v := range values {
		if !separatorCell.MatchString(v) {
			return false
		}
	}
	return len(values) > 0
}
