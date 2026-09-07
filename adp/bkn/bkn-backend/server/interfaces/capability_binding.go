// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "github.com/openbkn-ai/bkn-foundry/comm-go/audit"

// Capability types a knowledge network can bind. Skill and Function share one table because
// their binding semantics are identical: both store a reference, never the capability itself.
const (
	// AUDIT_TARGET_CAPABILITY_BINDING is the operation-audit target type of a binding row.
	AUDIT_TARGET_CAPABILITY_BINDING = "kn_capability_binding"

	CAPABILITY_TYPE_SKILL    = "skill"
	CAPABILITY_TYPE_FUNCTION = "function"
	// CAPABILITY_TYPE_MCP_TOOL is one tool of an MCP Server, addressed by ("mcp_tool", mcp_id,
	// tool_name).
	//
	// It is a type of its own rather than a function binding, even though an MCP Server is
	// assembled from tool box tools and its tool_configs carry their box_id/tool_id. Those record
	// where a tool came from; the callable contract is the MCP protocol's, which addresses tools
	// by name and runs them through /mcp/proxy/{mcp_id}/tool/call. ActionSource already draws the
	// same line with its type=mcp arm. Folding the two together would leave execute_tool unable
	// to tell which transport a binding meant.
	CAPABILITY_TYPE_MCP_TOOL = "mcp_tool"
	// CAPABILITY_TYPE_API is not a stored type. It is a counting bucket: function bindings whose
	// tool box is an openapi box. The rows still say "function"; only the statistics separate
	// them, because that is the split Studio shows.
	CAPABILITY_TYPE_API = "api"
)

// CapabilityBinding records that a Skill or a ToolBox tool belongs to a knowledge network.
// The master data stays in the execution factory; this row only holds the reference.
//
// A capability is identified by three parts: skill uses ("skill", "", skill_id) and function
// uses ("function", box_id, tool_id) — a tool_id is scoped to its box in the execution factory,
// so OwnerID is required to locate it.
type CapabilityBinding struct {
	ID             string `json:"id" mapstructure:"id"`
	KNID           string `json:"kn_id" mapstructure:"kn_id"`
	Branch         string `json:"branch" mapstructure:"branch"`
	CapabilityType string `json:"capability_type" mapstructure:"capability_type"`
	// OwnerID is the container the capability belongs to: the tool box for a function, the MCP
	// Server for an mcp_tool, empty for a skill. The wire name is box_id, matching what the
	// execution factory, Context Loader and Studio all call it; the column stays type-neutral,
	// which is what let mcp_tool arrive without a migration.
	OwnerID      string `json:"box_id,omitempty" mapstructure:"owner_id"`
	CapabilityID string `json:"capability_id" mapstructure:"capability_id"`
	// BoundAsBox marks a row produced by expanding a whole-box mount. It does not change the
	// binding semantics — the row is still an ordinary tool-level binding and can be released
	// on its own — it only lets the list view report how many tools of a box are not mounted.
	BoundAsBox bool   `json:"bound_as_box" mapstructure:"bound_as_box"`
	Comment    string `json:"comment,omitempty" mapstructure:"comment"`

	// Metadata backfilled from the execution factory on demand; never persisted here.
	Name string `json:"name,omitempty" mapstructure:"-"`
	// MetadataType is the kind of the owning tool box, "openapi" or "function", and is what
	// splits function bindings into the API and function lists. It is empty for a skill, for an
	// mcp_tool, and whenever the execution factory could not be reached — in the last case
	// metadata_available says so, and a reader must not take the blank for "function".
	MetadataType string `json:"metadata_type,omitempty" mapstructure:"-"`
	Description string `json:"description,omitempty" mapstructure:"-"`
	Status      string `json:"status,omitempty" mapstructure:"-"`
	OwnerName   string `json:"owner_name,omitempty" mapstructure:"-"`

	// Sources says why this capability is in the network: mounted explicitly, expanded from a
	// box, or used by an object type's logic property or an action type. A capability used by
	// the model but never mounted appears in the list with no manual source and cannot be
	// released — the way to remove it is to change what uses it.
	Sources []*CapabilitySource `json:"sources,omitempty" mapstructure:"-"`

	Creator    AccountInfo `json:"creator" mapstructure:"creator"`
	CreateTime int64       `json:"create_time" mapstructure:"create_time"`
	Updater    AccountInfo `json:"updater" mapstructure:"updater"`
	UpdateTime int64       `json:"update_time" mapstructure:"update_time"`
}

// IsValidCapabilityType reports whether the type is one this release knows how to bind.
// Validation is an exhaustive switch rather than a default-allow so that adding a type
// (mcp_server, for one) cannot silently skip its own write checks.
func IsValidCapabilityType(capabilityType string) bool {
	switch capabilityType {
	case CAPABILITY_TYPE_SKILL, CAPABILITY_TYPE_FUNCTION, CAPABILITY_TYPE_MCP_TOOL:
		return true
	default:
		return false
	}
}

// CapabilityBindingsQueryParams filters the capability list of one knowledge network branch.
type CapabilityBindingsQueryParams struct {
	PaginationQueryParameters
	KNID           string
	Branch         string
	CapabilityType string
	OwnerID        string
	CapabilityIDs  []string
	// MetadataType narrows function bindings to one kind of tool box. The value lives on the box
	// in the execution factory, not in the binding row, so the service resolves it to the set of
	// boxes with that kind and puts them in OwnerIDs — filtering the fetched page instead would
	// paginate over rows the filter then discards, and a page whose rows all belong to the other
	// kind would read as "none bound".
	MetadataType string
	// OwnerIDs restricts to a set of owners. A non-nil empty slice selects nothing; nil means no
	// restriction. It is set by the service, not parsed from the query string.
	OwnerIDs *[]string
	// WithDetail also fills description and status. Names alone cost one call per tool box and
	// one for all skills; the detail of a skill has to be read one skill at a time, so it is
	// asked for rather than always paid.
	WithDetail bool
}

// Where a capability in the list came from. A capability can have several at once.
const (
	// CAPABILITY_SOURCE_MANUAL is an explicit mount. It is the only source that can be released:
	// the others are consequences of the model and go away by changing it.
	CAPABILITY_SOURCE_MANUAL = "manual"
	// CAPABILITY_SOURCE_BOX marks a row produced by expanding a whole-box or whole-server mount.
	CAPABILITY_SOURCE_BOX = "box"
	// CAPABILITY_SOURCE_OBJECT_TYPE is a logic property of an object type using the tool.
	CAPABILITY_SOURCE_OBJECT_TYPE = "object_type"
	// CAPABILITY_SOURCE_ACTION_TYPE is an action type executing through the tool.
	CAPABILITY_SOURCE_ACTION_TYPE = "action_type"
)

// CapabilitySourceRef names one thing that brought a capability into the network.
type CapabilitySourceRef struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	// Property is the logic property using the tool, for an object_type source. Deleting a
	// property and deleting the object type are different repairs, so the reference says which.
	Property string `json:"property,omitempty"`
}

// CapabilitySource is one kind of origin, with what it points at.
type CapabilitySource struct {
	Kind string `json:"kind"`
	// Refs is empty for manual and box: those have nothing else to name.
	Refs []*CapabilitySourceRef `json:"refs,omitempty"`
}

// CAPABILITY_STATUS_MISSING marks a binding whose target is gone from the execution factory.
// Such a row is reported, never deleted: removing it silently would erase the only evidence that
// the network once pointed at something, and retrieval already skips it without saying so.
const CAPABILITY_STATUS_MISSING = "missing"

// CapabilityBoxSummary reports how much of a tool box this branch has mounted. It exists because
// a whole-box mount is expanded at write time and does not follow the box afterwards: without
// this, a tool added to the box later is invisible to the person who mounted it.
type CapabilityBoxSummary struct {
	BoxID string `json:"box_id"`
	// BoxName is empty when the execution factory could not be reached.
	BoxName string `json:"box_name,omitempty"`
	// BoxMissing marks a box that no longer exists; every binding under it is missing too.
	BoxMissing bool `json:"box_missing,omitempty"`
	// TotalTools counts the enabled tools currently in the box.
	TotalTools int `json:"total_tools"`
	// MountedTools counts those already bound to this branch.
	MountedTools int `json:"mounted_tools"`
	// UnmountedTools is what a one-click top-up would add.
	UnmountedTools int `json:"unmounted_tools"`
}

// CapabilityBindingsList is the list response for GET .../capabilities.
type CapabilityBindingsList struct {
	Entries    []*CapabilityBinding `json:"entries"`
	TotalCount int                  `json:"total_count"`
	// Boxes summarises the tool boxes behind the whole-box mounts on this page.
	Boxes []*CapabilityBoxSummary `json:"boxes,omitempty"`
	// MetadataAvailable is false when the execution factory could not be reached. The bindings
	// are still returned in full — the names are missing, not the memberships — and the flag
	// says so explicitly so an empty name is not read as a deleted capability.
	MetadataAvailable bool `json:"metadata_available"`
}

// AttachCapabilityEntry is one item of a mount request.
type AttachCapabilityEntry struct {
	CapabilityType string `json:"capability_type"`
	// OwnerID names the tool box for a function binding and is ignored for a skill.
	OwnerID      string `json:"box_id"`
	CapabilityID string `json:"capability_id"`
	Comment      string `json:"comment"`
	// AllTools mounts every enabled tool of the box named by OwnerID. It is expanded at write
	// time into one tool-level binding per tool, so the stored rows carry no box-level scope and
	// the read path never expands anything. Tools added to the box later are not inherited: a
	// shared box would otherwise let someone else widen this network's reach.
	AllTools bool `json:"all_tools"`
}

// AttachCapabilitiesReq mounts one or more capabilities onto a knowledge network branch.
type AttachCapabilitiesReq struct {
	Capabilities []*AttachCapabilityEntry `json:"capabilities"`
}

var (
	// CapabilityBindingSort maps the sort keys accepted on the query string to physical columns.
	CapabilityBindingSort = map[string]string{
		"create_time": "f_create_time",
		"update_time": "f_update_time",
	}
)

// GenerateCapabilityBindingAuditObject builds the audit object for mount and release operations.
func GenerateCapabilityBindingAuditObject(id string, name string) audit.AuditObject {
	return audit.AuditObject{
		Type: AUDIT_TARGET_CAPABILITY_BINDING,
		ID:   id,
		Name: name,
	}
}

// CapabilityReference is what a capability binding points at, with nothing else attached.
//
// The internal face answers in these rather than full bindings because Context Loader goes to the
// execution factory for names and statuses anyway: backfilling them here would add a service hop
// to every recall to produce data the caller is about to fetch itself.
type CapabilityReference struct {
	CapabilityType string `json:"capability_type"`
	// BoxID is the owning tool box of a function reference; absent for a skill.
	BoxID        string `json:"box_id,omitempty"`
	CapabilityID string `json:"capability_id"`
}

// CapabilityReferenceList is the internal resolve response.
type CapabilityReferenceList struct {
	Entries []*CapabilityReference `json:"entries"`
}

// CapabilitySkip explains why one declared capability could not be bound on import.
type CapabilitySkip struct {
	CapabilityType string `json:"capability_type"`
	// Name is what the model called it — the only part a reader can act on when the ids belong
	// to another environment.
	Name          string `json:"name,omitempty"`
	BoxName       string `json:"box_name,omitempty"`
	DeclaredID    string `json:"declared_id,omitempty"`
	DeclaredBoxID string `json:"declared_box_id,omitempty"`
	Reason        string `json:"reason"`
	Detail        string `json:"detail,omitempty"`
}

// CapabilityImportReport is what an import did with the declared capabilities. It travels in the
// import response: a skipped capability that is only logged makes an import look complete while
// the network quietly has nothing bound.
type CapabilityImportReport struct {
	Bound   int               `json:"bound"`
	Skipped []*CapabilitySkip `json:"skipped"`
}
