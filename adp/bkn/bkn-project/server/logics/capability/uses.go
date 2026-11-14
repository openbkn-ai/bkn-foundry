// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package capability

import (
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/bkn-specification/bkn"
)

// What a Use is.
const (
	USE_ACTION_TYPE    = "action_type"
	USE_LOGIC_PROPERTY = "logic_property"
	USE_CAPABILITY     = "capability"
)

// Use is a definition of a network that references a toolbox or Skill: an action type, a logic
// property of an object type (ID and Name are the object type's), or the network's capabilities.
type Use struct {
	Kind     string `json:"kind"`
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	Property string `json:"property,omitempty"`
}

// Uses names what in network references the toolbox or Skill id, as References reads them, and
// the name the network knows it by, if it records one: a toolbox gone from execution-factory can
// still be named and found where it is used.
func Uses(network *bkn.BknNetwork, kind Kind, id string) (name string, uses []Use) {
	if network == nil || id == "" {
		return "", nil
	}
	if kind == KIND_FUNCTION {
		for _, at := range network.ActionTypes {
			if s := at.ActionSource; s != nil && strings.EqualFold(s.Type, "tool") && s.BoxID == id {
				uses = append(uses, Use{Kind: USE_ACTION_TYPE, ID: at.ID, Name: at.Name})
			}
		}
		for _, ot := range network.ObjectTypes {
			for _, lp := range ot.LogicProperties {
				if s := lp.DataSource; s != nil && strings.EqualFold(s.Type, "tool") && s.BoxID == id {
					property := lp.DisplayName
					if property == "" {
						property = lp.Name
					}
					uses = append(uses, Use{Kind: USE_LOGIC_PROPERTY, ID: ot.ID, Name: ot.Name, Property: property})
				}
			}
		}
	}
	if c := network.Capabilities; c != nil {
		found := false
		if kind == KIND_FUNCTION {
			for _, f := range c.Functions {
				if f.BoxID == id {
					found = true
					if name == "" {
						name = f.BoxName
					}
				}
			}
		}
		if kind == KIND_SKILL {
			for _, s := range c.Skills {
				if s.ID == id {
					found = true
					if name == "" {
						name = s.Name
					}
				}
			}
		}
		if found {
			uses = append(uses, Use{Kind: USE_CAPABILITY})
		}
	}
	return name, uses
}
