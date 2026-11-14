// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package capability

import (
	"fmt"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/bkn-specification/bkn"
)

// A toolbox or Skill made again under the same name, as when a sample is installed again, has new
// ids, and definitions written before still name the old ones. A release moves them to the one the
// network uses now, or else to the one published item of the same name; RewritePackage then points
// the definitions at it as it does at a copy.

// Gone tells whether the item could not be read because it no longer exists.
func (e *ItemError) Gone() bool {
	return e.Operation == OPERATION_EXPORT && isNotFound(e.Err)
}

// ToolsOf lists, by tool id, the tools of toolbox boxID the network references, with the tool name
// it records where it records one.
func ToolsOf(network *bkn.BknNetwork, boxID string) map[string]string {
	tools := map[string]string{}
	add := func(id string, name string) {
		if id == "" {
			return
		}
		if tools[id] == "" {
			tools[id] = name
		}
	}
	for _, at := range network.ActionTypes {
		if s := at.ActionSource; s != nil && strings.EqualFold(s.Type, "tool") && s.BoxID == boxID {
			add(s.ToolID, s.ToolName)
		}
	}
	for _, ot := range network.ObjectTypes {
		for _, lp := range ot.LogicProperties {
			if s := lp.DataSource; s != nil && strings.EqualFold(s.Type, "tool") && s.BoxID == boxID {
				add(s.ToolID, "")
			}
		}
	}
	if c := network.Capabilities; c != nil {
		for _, f := range c.Functions {
			if f.BoxID == boxID {
				add(f.ToolID, f.ToolName)
			}
		}
	}
	return tools
}

// ToolIDs lists the tools of a toolbox item by name; a name two tools share maps to "".
func (it *Item) ToolIDs() (map[string]string, error) {
	doc, err := decodeJSON(it.Files[TOOLBOX_FILE])
	if err != nil {
		return nil, fmt.Errorf("read toolbox %s: %w", it.ID, err)
	}
	box, err := toolboxConfig(doc)
	if err != nil {
		return nil, err
	}
	byName := map[string]string{}
	tools, _ := box["tools"].([]any)
	for _, tool := range tools {
		name, id := stringField(tool, "name"), stringField(tool, "tool_id")
		if _, ok := byName[name]; ok {
			byName[name] = ""
			continue
		}
		byName[name] = id
	}
	return byName, nil
}

// RelinkFunction moves the network's references to toolbox oldID onto toolbox to: each tool to the
// one given for it in direct, else to the tool of to with the name the network records for it. It
// fails when a referenced tool has no such match.
func RelinkFunction(network *bkn.BknNetwork, oldID string, to *Item, direct map[string]string) (*Copy, error) {
	byName, err := to.ToolIDs()
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, id := range byName {
		if id != "" {
			known[id] = true
		}
	}
	c := &Copy{Kind: KIND_FUNCTION, OldID: oldID, NewID: to.ID, NewName: to.Name, ToolIDs: map[string]string{}}
	for toolID, name := range ToolsOf(network, oldID) {
		newID := direct[toolID]
		if newID == "" && name != "" {
			newID = byName[name]
		}
		if newID == "" || !known[newID] {
			return nil, fmt.Errorf("tool %s of toolbox %s has no match in toolbox %s", toolID, oldID, to.ID)
		}
		c.ToolIDs[toolID] = newID
	}
	return c, nil
}

// RelinkSkill moves the network's references to Skill oldID onto Skill newID of the same name.
func RelinkSkill(oldID string, newID string, name string) *Copy {
	return &Copy{Kind: KIND_SKILL, OldID: oldID, NewID: newID, NewName: name}
}
