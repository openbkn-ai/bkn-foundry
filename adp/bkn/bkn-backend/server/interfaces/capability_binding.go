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
	OwnerID        string `json:"owner_id,omitempty" mapstructure:"owner_id"`
	CapabilityID   string `json:"capability_id" mapstructure:"capability_id"`
	// BoundAsBox records how the row was created: by expanding a whole-box mount rather than by
	// naming the tool. It is provenance, not current coverage — a tool bound individually and
	// later included in a whole-box mount keeps the flag false, because that first gesture is
	// still what created it.
	//
	// Anything asking "which tools of this box are bound" must therefore group by box, not
	// filter on this flag. The row is an ordinary tool-level binding either way and can be
	// released on its own.
	BoundAsBox bool   `json:"bound_as_box" mapstructure:"bound_as_box"`
	Comment    string `json:"comment,omitempty" mapstructure:"comment"`

	// Metadata backfilled from the execution factory on demand; never persisted here.
	Name        string `json:"name,omitempty" mapstructure:"-"`
	Description string `json:"description,omitempty" mapstructure:"-"`
	Status      string `json:"status,omitempty" mapstructure:"-"`
	OwnerName   string `json:"owner_name,omitempty" mapstructure:"-"`

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
	case CAPABILITY_TYPE_SKILL, CAPABILITY_TYPE_FUNCTION:
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
	// WithDetail also fills description and status. Names alone cost one call per tool box and
	// one for all skills; the detail of a skill has to be read one skill at a time, so it is
	// asked for rather than always paid.
	WithDetail bool
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
	OwnerID        string `json:"owner_id"`
	CapabilityID   string `json:"capability_id"`
	Comment        string `json:"comment"`
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
