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
	// BoundAsBox marks a row produced by expanding a whole-box mount. It does not change the
	// binding semantics — the row is still an ordinary tool-level binding and can be released
	// on its own — it only lets the list view report how many tools of a box are not mounted.
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
}

// CapabilityBindingsList is the list response for GET .../capabilities.
type CapabilityBindingsList struct {
	Entries    []*CapabilityBinding `json:"entries"`
	TotalCount int                  `json:"total_count"`
}

// AttachCapabilityEntry is one item of a mount request.
type AttachCapabilityEntry struct {
	CapabilityType string `json:"capability_type"`
	OwnerID        string `json:"owner_id"`
	CapabilityID   string `json:"capability_id"`
	Comment        string `json:"comment"`
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
