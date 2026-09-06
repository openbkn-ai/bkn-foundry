// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package permdata is the open-core decision socket for object-property access.
// Community builds leave it empty and derive every property from the caller's
// object-type permission. Enterprise assemblies register the sparse property
// resolver implemented in openbkn-ee.
package permdata

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/openbkn-ai/licverify"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/bkn-foundry/comm-go/propertyaccess"
)

const (
	// Capability deliberately reuses the existing Enterprise authorization
	// bundle. It is a display name, not a feature-key licensing decision.
	Capability = "perm_object_level"

	SourceObjectType = "object_type"
	SourceProperty   = "property"
)

// ErrInvalidResolution means an extension returned an incomplete, reordered,
// or unknown decision. The caller must fail closed and must not use a partial
// response.
var ErrInvalidResolution = errors.New("invalid property access resolution")

// Request is one batched decision. BaseLevel is already derived from core's
// object-type/knowledge-network operations and is the upper bound for every
// property in the item.
type Request struct {
	AccessorID string
	Items      []RequestItem
}

type RequestItem struct {
	ObjectTypeRef string
	Properties    []string
	BaseLevel     propertyaccess.Level
}

// Resolution is the property layer's answer before core applies BaseLevel.
// A resolver must return one entry and property for every request element, in
// the same order. Explicit=false means no property grant matched and therefore
// Level must be full (the neutral, non-restricting value).
type Resolution struct {
	Entries []ResolutionEntry
}

type ResolutionEntry struct {
	ObjectTypeRef string
	Properties    []ResolvedProperty
}

type ResolvedProperty struct {
	Name     string
	Level    propertyaccess.Level
	Explicit bool
}

// Response is the effective, clamped result returned by bkn-safe.
type Response struct {
	Entries []DecisionEntry `json:"entries"`
}

type DecisionEntry struct {
	ObjectTypeRef string             `json:"object_type_ref"`
	Properties    []PropertyDecision `json:"properties"`
}

type PropertyDecision struct {
	Name   string               `json:"name"`
	Level  propertyaccess.Level `json:"level"`
	Source string               `json:"source"`
}

// Resolver is implemented only by the enterprise code line. Resolve must be
// safe for concurrent use and answer the whole request in one call.
type Resolver interface {
	Resolve(ctx context.Context, request Request) (Resolution, error)
}

var implementation atomic.Value // Resolver
var minEdition licverify.Edition

// Register installs the Enterprise resolver during process assembly. It is
// unconditional with respect to the current certificate; Resolve checks the
// live edition on every request so activation and downgrade need no restart.
func Register(min licverify.Edition, resolver Resolver) {
	if resolver == nil {
		panic("permdata: Register(nil)")
	}
	if load() != nil {
		panic("permdata: resolver already registered")
	}
	entitlement.MustBeAssembling("permdata")
	entitlement.MarkAssembled(Capability, min)
	minEdition = min
	implementation.Store(resolver)
}

func Registered() bool { return load() != nil }

func Available() bool {
	return load() != nil && entitlement.AtLeast(minEdition)
}

// Resolve returns Community fallback decisions when the socket is empty or
// the live edition is below the registered minimum. Enterprise results are
// shape-validated and clamped against BaseLevel before they leave core.
func Resolve(ctx context.Context, request Request) (Response, error) {
	if err := validateRequest(request); err != nil {
		return Response{}, err
	}
	resolver := load()
	if resolver == nil || !entitlement.AtLeast(minEdition) {
		return Fallback(request)
	}
	resolution, err := resolver.Resolve(ctx, request)
	if err != nil {
		return Response{}, err
	}
	return apply(request, resolution)
}

// Fallback derives decisions exclusively from core's BaseLevel. It is also
// used for inactive accessors so no enterprise resolver is consulted for an
// identity that core has already rejected.
func Fallback(request Request) (Response, error) {
	if err := validateRequest(request); err != nil {
		return Response{}, err
	}
	response := Response{Entries: make([]DecisionEntry, 0, len(request.Items))}
	for _, item := range request.Items {
		entry := DecisionEntry{
			ObjectTypeRef: item.ObjectTypeRef,
			Properties:    make([]PropertyDecision, 0, len(item.Properties)),
		}
		for _, name := range item.Properties {
			entry.Properties = append(entry.Properties, PropertyDecision{
				Name: name, Level: item.BaseLevel, Source: SourceObjectType,
			})
		}
		response.Entries = append(response.Entries, entry)
	}
	return response, nil
}

func apply(request Request, resolution Resolution) (Response, error) {
	if len(resolution.Entries) != len(request.Items) {
		return Response{}, invalid("entry count %d, want %d", len(resolution.Entries), len(request.Items))
	}
	response := Response{Entries: make([]DecisionEntry, 0, len(request.Items))}
	for itemIndex, item := range request.Items {
		resolvedEntry := resolution.Entries[itemIndex]
		if resolvedEntry.ObjectTypeRef != item.ObjectTypeRef {
			return Response{}, invalid("entry %d object_type_ref %q, want %q", itemIndex, resolvedEntry.ObjectTypeRef, item.ObjectTypeRef)
		}
		if len(resolvedEntry.Properties) != len(item.Properties) {
			return Response{}, invalid("entry %d property count %d, want %d", itemIndex, len(resolvedEntry.Properties), len(item.Properties))
		}

		entry := DecisionEntry{
			ObjectTypeRef: item.ObjectTypeRef,
			Properties:    make([]PropertyDecision, 0, len(item.Properties)),
		}
		for propertyIndex, name := range item.Properties {
			resolved := resolvedEntry.Properties[propertyIndex]
			if resolved.Name != name {
				return Response{}, invalid("entry %d property %d name %q, want %q", itemIndex, propertyIndex, resolved.Name, name)
			}
			if !resolved.Level.Valid() {
				return Response{}, invalid("entry %d property %q has level %q", itemIndex, name, resolved.Level)
			}
			if !resolved.Explicit && resolved.Level != propertyaccess.Full {
				return Response{}, invalid("entry %d property %q has an implicit non-full level", itemIndex, name)
			}

			effective, err := propertyaccess.Min(item.BaseLevel, resolved.Level)
			if err != nil {
				return Response{}, invalid("entry %d property %q: %v", itemIndex, name, err)
			}
			source := SourceObjectType
			if resolved.Explicit {
				comparison, err := propertyaccess.Compare(resolved.Level, item.BaseLevel)
				if err != nil {
					return Response{}, invalid("entry %d property %q: %v", itemIndex, name, err)
				}
				if comparison <= 0 {
					source = SourceProperty
				}
			}
			entry.Properties = append(entry.Properties, PropertyDecision{
				Name: name, Level: effective, Source: source,
			})
		}
		response.Entries = append(response.Entries, entry)
	}
	return response, nil
}

func validateRequest(request Request) error {
	if request.AccessorID == "" {
		return invalid("accessor_id is empty")
	}
	seenObjects := make(map[string]struct{}, len(request.Items))
	for itemIndex, item := range request.Items {
		if item.ObjectTypeRef == "" {
			return invalid("entry %d object_type_ref is empty", itemIndex)
		}
		if _, duplicate := seenObjects[item.ObjectTypeRef]; duplicate {
			return invalid("duplicate object_type_ref %q", item.ObjectTypeRef)
		}
		seenObjects[item.ObjectTypeRef] = struct{}{}
		if !item.BaseLevel.Valid() {
			return invalid("entry %d has base level %q", itemIndex, item.BaseLevel)
		}
		seenProperties := make(map[string]struct{}, len(item.Properties))
		for _, name := range item.Properties {
			if name == "" {
				return invalid("entry %d contains an empty property name", itemIndex)
			}
			if _, duplicate := seenProperties[name]; duplicate {
				return invalid("entry %d contains duplicate property %q", itemIndex, name)
			}
			seenProperties[name] = struct{}{}
		}
	}
	return nil
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidResolution, fmt.Sprintf(format, args...))
}

func load() Resolver {
	value := implementation.Load()
	if value == nil {
		return nil
	}
	resolver, _ := value.(Resolver)
	return resolver
}

func reset() {
	implementation = atomic.Value{}
	minEdition = ""
}
