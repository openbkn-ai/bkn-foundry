// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"
	"encoding/json"
)

// Kinds of registered IDs: the draft's item kinds, and properties of object types.
const (
	ID_KIND_OBJECT_TYPES   = DRAFT_KIND_OBJECT_TYPES
	ID_KIND_VALUE_TYPES    = DRAFT_KIND_VALUE_TYPES
	ID_KIND_RELATION_TYPES = DRAFT_KIND_RELATION_TYPES
	ID_KIND_PROPERTIES     = "properties"
)

// Origins of registered IDs.
const (
	// ID_ORIGIN_SEMANTIC is an ID made from the English short name semantic understanding gave.
	ID_ORIGIN_SEMANTIC = "semantic"
	// ID_ORIGIN_FALLBACK is a short-hash placeholder, made when semantic understanding gave no
	// usable short name; a later draft replaces it with one until a confirmation locks it.
	ID_ORIGIN_FALLBACK = "fallback"
)

// IDKey names the item an ID is registered for.
type IDKey struct {
	Kind string
	// SourceKey is the resource the item is drafted from, and for a property also its field:
	// "<resource ID>/<field name>".
	SourceKey string
}

// RegisteredID is the ID an item drafted from a physical name that is not ASCII got. Every later
// draft of the project gives the item this ID, so it does not change from draft to draft.
type RegisteredID struct {
	ProjectID string
	IDKey
	ID     string
	Origin string
	// Basis is what the ID was made from, such as the physical name and the short name, JSON.
	Basis      json.RawMessage
	Locked     bool
	CreateTime int64
	UpdateTime int64
}

//go:generate mockgen -source ../interfaces/id_registry_access.go -destination ../interfaces/mock/mock_id_registry_access.go
type IDRegistryAccess interface {
	// ListIDs returns the project's registered IDs.
	ListIDs(ctx context.Context, projectID string) ([]*RegisteredID, error)
	// RecordIDs registers ids. An item registered already keeps its ID, except that an unlocked
	// fallback is replaced by a semantic ID.
	RecordIDs(ctx context.Context, ids []*RegisteredID) error
	// LockIDs locks the project's IDs of keys, so they are never replaced again.
	LockIDs(ctx context.Context, projectID string, keys []IDKey, updateTime int64) error
}
