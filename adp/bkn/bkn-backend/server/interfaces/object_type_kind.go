// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"
	"database/sql"
	"slices"
)

// ObjectTypeKindEvent makes an object type an event class: each object is an event of the
// network's event ledger, and the platform keeps the class's data source, properties and keys.
const ObjectTypeKindEvent = "event"

// ObjectTypeEvent is the configuration of the event kind.
type ObjectTypeEvent struct {
	// Source is rule or report, fixed once the class exists.
	Source string `json:"source" mapstructure:"source"`
	// Subject is the object type the events are about.
	Subject string `json:"subject,omitempty" mapstructure:"subject"`
	// Severity is the default severity of the class's events.
	Severity string `json:"severity,omitempty" mapstructure:"severity"`
	// MergeHours is the window in which a report about the same object is a duplicate.
	MergeHours float64 `json:"merge_hours,omitempty" mapstructure:"merge_hours"`
	// HandlingNote says what to do about an event of the class.
	HandlingNote string `json:"handling_note,omitempty" mapstructure:"handling_note"`
	// HandlingAction is the action type that handles an event of the class, if any.
	HandlingAction *ObjectTypeEventHandling `json:"handling_action,omitempty" mapstructure:"handling_action"`
}

// ObjectTypeEventHandling is the action type that handles the events of a class, and how: auto
// runs it on the object of each event a check newly finds, suggest only offers it to the person
// handling an event.
type ObjectTypeEventHandling struct {
	ActionTypeID string `json:"action_type_id" mapstructure:"action_type_id"`
	Mode         string `json:"mode" mapstructure:"mode"`
	// Parameters gives inputs of the action type a value, by the input's name: run by itself, the
	// action needs each of its inputs given here; suggested, these are the values offered.
	Parameters map[string]ObjectTypeEventHandlingParameter `json:"parameters,omitempty" mapstructure:"parameters"`
}

// ObjectTypeEventHandlingParameter is the value of an input of a handling action: a constant
// (value_from const), or the property of the object the event is about that value names
// (value_from property).
type ObjectTypeEventHandlingParameter struct {
	ValueFrom string `json:"value_from" mapstructure:"value_from"`
	Value     any    `json:"value" mapstructure:"value"`
}

// HasKind reports whether the object type has the kind.
func (ot *ObjectType) HasKind(kind string) bool {
	return ot != nil && slices.Contains(ot.Kinds, kind)
}

// ObjectTypeKind is the platform behaviour one kind gives an object type. The module that
// provides the kind registers it; the object type service refuses a kind no module provides.
type ObjectTypeKind interface {
	// Available reports whether object types of the kind may be created or changed now, as the
	// license allows. Ones that exist are read and deleted either way.
	Available() bool
	// Complete checks the kind's configuration and fills the definitions the platform keeps for
	// the kind. stored is the object type being replaced, nil for a new one. A definition the
	// caller wrote that differs from what the platform keeps is refused. It reads nothing.
	Complete(ctx context.Context, objectType *ObjectType, stored *ObjectType) error
	// Check checks what the kind depends on in the network, before anything is written.
	Check(ctx context.Context, objectType *ObjectType, stored *ObjectType) error
	// Saved keeps what the kind makes beside the object type, in the transaction that saved it.
	// stored is the object type it replaced, nil for a new one.
	Saved(ctx context.Context, tx *sql.Tx, objectType *ObjectType, stored *ObjectType) error
	// OwnedRelationTypes are the relation types the kind keeps for the object type, as the kind
	// writes them. They start from the object type. They cannot be changed through the relation
	// type API or an import, nor are they exported, and they do not keep the object type from
	// being deleted.
	OwnedRelationTypes(objectType *ObjectType) []*RelationType
	// Deleting refuses the deletion of an object type of the kind, or removes what the kind
	// keeps beside it, in the transaction that deletes it.
	Deleting(ctx context.Context, tx *sql.Tx, objectType *ObjectType) error
	// PlatformResource reports whether the Vega resource bound to the object type objectTypeID of
	// the network knID is one the platform keeps for the kind. The platform grants such a
	// resource to the network's proxy, and the caller needs no permission of their own on it.
	PlatformResource(knID, objectTypeID, resourceID string) bool
}
