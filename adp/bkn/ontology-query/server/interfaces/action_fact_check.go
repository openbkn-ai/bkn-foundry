// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

// ActionFactCheck is a read-only, single-target check requested by the
// governance service. Network and action IDs come from the route, not the body.
type ActionFactCheck struct {
	KNID             string                `json:"-"`
	ActionTypeID     string                `json:"-"`
	Initiator        string                `json:"initiator"`
	DefinitionDigest string                `json:"definition_digest"`
	TargetIdentity   map[string]any        `json:"target_identity"`
	DynamicParams    map[string]any        `json:"dynamic_params,omitempty"`
	ObjectInput      map[string]any        `json:"object_input,omitempty"`
	RelationTarget   *ActionRelationTarget `json:"relation_target,omitempty"`
}
