// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

// A kinetic need links to its implementation in the project's live network. The link holds only
// the implementation's ids: its name, status, version and definition are read live, as the
// caller, whenever the need is read.

// Kinds of implementations a need links to.
const (
	LOGIC_LINK_OBJECT_METRIC = "object_metric"
	LOGIC_LINK_FUNCTION      = "function"
	LOGIC_LINK_SKILL         = "skill"
	LOGIC_LINK_MCP_TOOL      = "mcp_tool"
	LOGIC_LINK_ACTION_TYPE   = "action_type"
)

// LogicLinkTypeOf is the kind of implementation a need of kind links to; empty for one that links
// to none, an application. An API is a function bound from an OpenAPI description.
func LogicLinkTypeOf(kind string) string {
	switch kind {
	case LOGIC_NEED_METRIC:
		return LOGIC_LINK_OBJECT_METRIC
	case LOGIC_NEED_FUNCTION, LOGIC_NEED_API:
		return LOGIC_LINK_FUNCTION
	case LOGIC_NEED_SKILL:
		return LOGIC_LINK_SKILL
	case LOGIC_NEED_MCP_TOOL:
		return LOGIC_LINK_MCP_TOOL
	case LOGIC_NEED_ACTION_TYPE:
		return LOGIC_LINK_ACTION_TYPE
	}
	return ""
}

// RequirementLogicLink is the implementation a need is linked to: an object metric, Skill or action
// type by its id; a function or API by its toolbox and id; an MCP tool by its MCP server and name.
type RequirementLogicLink struct {
	Type  string      `json:"type"`
	ID    string      `json:"id"`
	BoxID string      `json:"box_id,omitempty"`
	By    AccountInfo `json:"by"`
	At    int64       `json:"at"`
}

// Why a linked implementation cannot be read: gone from the live network, not readable by the
// caller, or not answered, as when the network or its metric feature is not there.
const (
	LOGIC_IMPLEMENTATION_MISSING     = "missing"
	LOGIC_IMPLEMENTATION_FORBIDDEN   = "forbidden"
	LOGIC_IMPLEMENTATION_UNAVAILABLE = "unavailable"
)

// LogicNeedImplementation is a linked implementation as the live network has it now.
type LogicNeedImplementation struct {
	Available           bool   `json:"available"`
	Reason              string `json:"reason,omitempty"`
	Name                string `json:"name,omitempty"`
	Description         string `json:"description,omitempty"`
	Status              string `json:"status,omitempty"`
	Version             int    `json:"version,omitempty"`
	OwnerObjectTypeID   string `json:"owner_object_type_id,omitempty"`
	OwnerObjectTypeName string `json:"owner_object_type_name,omitempty"`
}

// LogicNeedView is a kinetic need with its implementation read live, and the object types it is
// about that the live network has, which implementing it needs.
type LogicNeedView struct {
	*RequirementLogicNeed
	Implementation    *LogicNeedImplementation `json:"implementation,omitempty"`
	LiveObjectTypeIDs []string                 `json:"live_object_type_ids"`
	// Unconnected are the pairs of its object types that no adopted path of at most two relations
	// joins in the draft: computing it may find nothing to join them by.
	Unconnected []ScenarioUnconnected `json:"unconnected"`
}

// LOGIC_TARGET_UNAVAILABLE says the live network's object types could not be read.
const LOGIC_TARGET_UNAVAILABLE = "unavailable"

// LogicNeedsView is a project's kinetic needs as the kinetic modeling page reads them. NetworkID is
// the live network implementations are made in and read from. A need is implemented there on its
// live object types: until a release puts its object types in the live network, it is only
// recorded. Reason says the network's object types could not be read.
type LogicNeedsView struct {
	UpdateTime int64  `json:"update_time"`
	NetworkID  string `json:"network_id"`
	// Released says the project was ever released: before, no implementation can be made or read
	// live for it, and that is no failure to read.
	Released bool             `json:"released"`
	Reason   string           `json:"reason,omitempty"`
	Needs    []*LogicNeedView `json:"needs"`
}

// Why a candidate is offered for a need.
const (
	LOGIC_CANDIDATE_SAME_NAME   = "same_name"
	LOGIC_CANDIDATE_SIMILAR     = "similar_name"
	LOGIC_CANDIDATE_SAME_OBJECT = "same_object_type"
)

// LogicNeedCandidate is an implementation of the live network a need could link to.
type LogicNeedCandidate struct {
	Type              string   `json:"type"`
	ID                string   `json:"id"`
	BoxID             string   `json:"box_id,omitempty"`
	Name              string   `json:"name"`
	Description       string   `json:"description,omitempty"`
	OwnerObjectTypeID string   `json:"owner_object_type_id,omitempty"`
	Reasons           []string `json:"reasons"`
}

// LogicNeedCandidates are the candidates of a need, the likeliest first.
type LogicNeedCandidates struct {
	NetworkID  string                `json:"network_id"`
	Reason     string                `json:"reason,omitempty"`
	Candidates []*LogicNeedCandidate `json:"candidates"`
}

// RequirementLogicLinkEdit links a need to an implementation of the live network, written over the
// specification as read at UpdateTime.
type RequirementLogicLinkEdit struct {
	UpdateTime int64  `json:"update_time"`
	Type       string `json:"type"`
	ID         string `json:"id"`
	BoxID      string `json:"box_id"`
}
