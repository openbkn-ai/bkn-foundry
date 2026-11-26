// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logics

import (
	"context"
	"sort"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// objectTypeKinds holds the kinds the assembled modules provide. A module registers its kind
// once at startup; the object type and relation type services read them on every save.
var objectTypeKinds struct {
	sync.RWMutex
	kinds map[string]interfaces.ObjectTypeKind
}

// RegisterObjectTypeKind makes a kind of object type available under its name.
func RegisterObjectTypeKind(name string, kind interfaces.ObjectTypeKind) {
	objectTypeKinds.Lock()
	defer objectTypeKinds.Unlock()
	if objectTypeKinds.kinds == nil {
		objectTypeKinds.kinds = map[string]interfaces.ObjectTypeKind{}
	}
	if kind == nil {
		delete(objectTypeKinds.kinds, name)
		return
	}
	objectTypeKinds.kinds[name] = kind
}

// AnyObjectTypeKind reports whether a module provides a kind. Without one, no object type has
// platform behaviours, and nothing about kinds needs to be read.
func AnyObjectTypeKind() bool {
	objectTypeKinds.RLock()
	defer objectTypeKinds.RUnlock()
	return len(objectTypeKinds.kinds) > 0
}

// ObjectTypeKindNamed is the kind registered under name, nil when no module provides it.
func ObjectTypeKindNamed(name string) interfaces.ObjectTypeKind {
	objectTypeKinds.RLock()
	defer objectTypeKinds.RUnlock()
	return objectTypeKinds.kinds[name]
}

// KindsOf are the registered kinds of an object type, in the order it lists them. A kind no
// module provides is left out.
func KindsOf(objectType *interfaces.ObjectType) []interfaces.ObjectTypeKind {
	if objectType == nil || len(objectType.Kinds) == 0 {
		return nil
	}
	kinds := make([]interfaces.ObjectTypeKind, 0, len(objectType.Kinds))
	for _, name := range objectType.Kinds {
		if kind := ObjectTypeKindNamed(name); kind != nil {
			kinds = append(kinds, kind)
		}
	}
	return kinds
}

// OwnedRelationTypes are the relation types the kinds of an object type keep for it, by ID.
func OwnedRelationTypes(objectType *interfaces.ObjectType) []*interfaces.RelationType {
	var owned []*interfaces.RelationType
	for _, kind := range KindsOf(objectType) {
		for _, relationType := range kind.OwnedRelationTypes(objectType) {
			if relationType != nil && relationType.RTID != "" {
				owned = append(owned, relationType)
			}
		}
	}
	sort.Slice(owned, func(i, j int) bool { return owned[i].RTID < owned[j].RTID })
	return owned
}

// OwnedRelationTypeIDs are the IDs of the relation types the kinds of an object type keep for it.
func OwnedRelationTypeIDs(objectType *interfaces.ObjectType) []string {
	owned := OwnedRelationTypes(objectType)
	ids := make([]string, 0, len(owned))
	for _, relationType := range owned {
		ids = append(ids, relationType.RTID)
	}
	return ids
}

// KeepsResource reports whether one of the object type's kinds keeps the resource it is bound
// to: the caller needs no permission of their own on it, and the platform grants it to the
// network's proxy. Only an object type that lists the kind counts, whatever the resource's ID.
func KeepsResource(objectType *interfaces.ObjectType) bool {
	if objectType == nil || objectType.DataSource == nil ||
		objectType.DataSource.Type != interfaces.DATA_SOURCE_TYPE_RESOURCE {
		return false
	}
	for _, kind := range KindsOf(objectType) {
		if kind.PlatformResource(objectType.KNID, objectType.OTID, objectType.DataSource.ID) {
			return true
		}
	}
	return false
}

// keptByKindKey marks a context whose writes are the kinds' own: the relation types a kind keeps
// may be written under it.
type keptByKindKey struct{}

// WithKindWrite is ctx for a kind writing what it keeps beside an object type.
func WithKindWrite(ctx context.Context) context.Context {
	return context.WithValue(ctx, keptByKindKey{}, true)
}

// IsKindWrite reports whether ctx is a kind writing what it keeps.
func IsKindWrite(ctx context.Context) bool {
	kept, _ := ctx.Value(keptByKindKey{}).(bool)
	return kept
}
