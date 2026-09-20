// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package rowfilter is the open-core decision socket for object-instance row
// filtering. Community builds leave it empty and receive the non-restricting
// TRUE plan; openbkn-ee registers the Enterprise policy resolver.
package rowfilter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync/atomic"

	"github.com/openbkn-ai/licverify"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
)

// Capability deliberately reuses the existing Enterprise authorization
// bundle. It is a display name, not a new licence feature key.
const Capability = "perm_object_level"

// ErrInvalidPlan means an extension returned a plan outside the deliberately
// small, safe predicate language. Callers must fail closed and never compile
// a partial or arbitrary expression.
var ErrInvalidPlan = errors.New("invalid row filter plan")

// Caller is the server-derived identity context available to policy
// resolution. Roles and department ranges are populated only by bkn-safe's
// trusted directory/authz resolver; request payloads must never supply them.
type Caller struct {
	UserID              string
	RoleIDs             []string
	DirectDepartmentIDs []string
	DepartmentTreeIDs   []string
}

// Request is one object-type row-filter decision for a trusted caller.
type Request struct {
	ObjectTypeRef string
	Caller        Caller
}

// ValueType is the intentionally small scalar vocabulary allowed in an IN
// predicate. It mirrors the v1 value_set policy contract and prevents the EE
// implementation from smuggling a backend-specific expression into core.
type ValueType string

const (
	ValueString  ValueType = "string"
	ValueInteger ValueType = "integer"
	ValueBoolean ValueType = "boolean"
)

// Value is a typed scalar used by an IN predicate. Only the member selected
// by Type is meaningful.
type Value struct {
	Type    ValueType
	String  string
	Integer int64
	Boolean bool
}

// PredicateKind is the v1 effective-row-filter language. User queries are
// ANDed with this complete plan by the execution layer; a policy resolver can
// only produce a constant, one exact IN branch, or an OR of such branches.
type PredicateKind string

const (
	PredicateTrue  PredicateKind = "true"
	PredicateFalse PredicateKind = "false"
	PredicateIn    PredicateKind = "in"
	PredicateOr    PredicateKind = "or"
)

// Predicate is an abstract, backend-neutral effective row filter. Property is
// required only for IN; Values are always typed. OR contains one or more
// normalized children and is never used for an empty, singleton, TRUE, or
// FALSE expression.
type Predicate struct {
	Kind       PredicateKind
	Property   string
	Values     []Value
	Predicates []Predicate
}

// Plan is the normalized effective filter used by every object read path.
// EffectiveRowFilterDigest is derived by core after validation; an extension
// cannot choose or forge it.
type Plan struct {
	Predicate Predicate
}

// Response is the complete, validated result handed to execution code.
type Response struct {
	Plan                     Plan
	EffectiveRowFilterDigest string
}

// Resolver is implemented only by the Enterprise code line. Resolve must be
// safe for concurrent use and return the complete effective plan for request.
type Resolver interface {
	Resolve(ctx context.Context, request Request) (Plan, error)
}

var implementation atomic.Value // Resolver
var minEdition licverify.Edition

// Register installs the Enterprise resolver during process assembly. It is
// unconditional with respect to the live certificate; Resolve evaluates the
// tier on every call so activation and downgrade need no restart.
func Register(min licverify.Edition, resolver Resolver) {
	if resolver == nil {
		panic("rowfilter: Register(nil)")
	}
	if load() != nil {
		panic("rowfilter: resolver already registered")
	}
	entitlement.MustBeAssembling("rowfilter")
	entitlement.MarkAssembled(Capability, min)
	minEdition = min
	implementation.Store(resolver)
}

func Registered() bool { return load() != nil }

func Available() bool {
	return load() != nil && entitlement.AtLeast(minEdition)
}

// Resolve returns the Community TRUE fallback when no Enterprise resolver is
// assembled or the live edition is below its tier. Resolver failures and
// invalid plans are returned so the execution layer can fail closed.
func Resolve(ctx context.Context, request Request) (Response, error) {
	if err := validateRequest(request); err != nil {
		return Response{}, err
	}
	resolver := load()
	if resolver == nil || !entitlement.AtLeast(minEdition) {
		return Fallback(request)
	}
	plan, err := resolver.Resolve(ctx, request)
	if err != nil {
		return Response{}, err
	}
	return responseFor(request.ObjectTypeRef, plan)
}

// Fallback is the documented Community/no-license compatibility behavior: no
// policy exists at this layer, so the effective row filter is TRUE. It never
// bypasses the separate object-type query_data authorization check.
func Fallback(request Request) (Response, error) {
	if err := validateRequest(request); err != nil {
		return Response{}, err
	}
	return responseFor(request.ObjectTypeRef, Plan{Predicate: Predicate{Kind: PredicateTrue}})
}

func responseFor(objectTypeRef string, plan Plan) (Response, error) {
	normalized, err := Normalize(plan)
	if err != nil {
		return Response{}, err
	}
	canonical, err := json.Marshal(struct {
		ObjectTypeRef string    `json:"object_type_ref"`
		Predicate     Predicate `json:"predicate"`
	}{ObjectTypeRef: objectTypeRef, Predicate: normalized.Predicate})
	if err != nil {
		return Response{}, fmt.Errorf("encode normalized row filter: %w", err)
	}
	digest := sha256.Sum256(canonical)
	return Response{
		Plan:                     normalized,
		EffectiveRowFilterDigest: "sha256:" + hex.EncodeToString(digest[:]),
	}, nil
}

// Normalize validates and canonicalizes the v1 plan. Canonical plans make the
// digest semantic: differently ordered but equivalent IN/OR branches produce
// exactly the same effective_row_filter_digest.
func Normalize(plan Plan) (Plan, error) {
	predicate, err := normalizePredicate(plan.Predicate)
	if err != nil {
		return Plan{}, err
	}
	return Plan{Predicate: predicate}, nil
}

func normalizePredicate(predicate Predicate) (Predicate, error) {
	switch predicate.Kind {
	case PredicateTrue, PredicateFalse:
		if predicate.Property != "" || len(predicate.Values) != 0 || len(predicate.Predicates) != 0 {
			return Predicate{}, invalid("%s predicate carries fields", predicate.Kind)
		}
		return Predicate{Kind: predicate.Kind}, nil
	case PredicateIn:
		if predicate.Property == "" || len(predicate.Predicates) != 0 {
			return Predicate{}, invalid("in predicate must have one property and no child predicates")
		}
		if len(predicate.Values) == 0 || len(predicate.Values) > 100 {
			return Predicate{}, invalid("in predicate has %d values, want 1..100", len(predicate.Values))
		}
		values := append([]Value(nil), predicate.Values...)
		for _, value := range values {
			if !value.valid() {
				return Predicate{}, invalid("in predicate has invalid %q value", value.Type)
			}
		}
		sort.Slice(values, func(i, j int) bool { return valueKey(values[i]) < valueKey(values[j]) })
		for i := 1; i < len(values); i++ {
			if valueKey(values[i-1]) == valueKey(values[i]) {
				return Predicate{}, invalid("in predicate contains a duplicate value")
			}
		}
		return Predicate{Kind: PredicateIn, Property: predicate.Property, Values: values}, nil
	case PredicateOr:
		if predicate.Property != "" || len(predicate.Values) != 0 || len(predicate.Predicates) == 0 {
			return Predicate{}, invalid("or predicate must contain child predicates only")
		}
		children := make([]Predicate, 0, len(predicate.Predicates))
		for _, child := range predicate.Predicates {
			normalized, err := normalizePredicate(child)
			if err != nil {
				return Predicate{}, err
			}
			switch normalized.Kind {
			case PredicateTrue:
				return Predicate{Kind: PredicateTrue}, nil
			case PredicateFalse:
				continue
			case PredicateOr:
				children = append(children, normalized.Predicates...)
			default:
				children = append(children, normalized)
			}
		}
		if len(children) == 0 {
			return Predicate{Kind: PredicateFalse}, nil
		}
		sort.Slice(children, func(i, j int) bool { return predicateKey(children[i]) < predicateKey(children[j]) })
		unique := children[:0]
		for _, child := range children {
			if len(unique) == 0 || predicateKey(unique[len(unique)-1]) != predicateKey(child) {
				unique = append(unique, child)
			}
		}
		if len(unique) == 1 {
			return unique[0], nil
		}
		return Predicate{Kind: PredicateOr, Predicates: unique}, nil
	default:
		return Predicate{}, invalid("unknown predicate kind %q", predicate.Kind)
	}
}

func (value Value) valid() bool {
	switch value.Type {
	case ValueString:
		return value.Integer == 0 && !value.Boolean
	case ValueInteger:
		return value.String == "" && !value.Boolean
	case ValueBoolean:
		return value.String == "" && value.Integer == 0
	default:
		return false
	}
}

func valueKey(value Value) string {
	switch value.Type {
	case ValueString:
		encoded, _ := json.Marshal(value.String)
		return string(value.Type) + ":" + string(encoded)
	case ValueInteger:
		return fmt.Sprintf("%s:%020d", value.Type, value.Integer)
	case ValueBoolean:
		return fmt.Sprintf("%s:%t", value.Type, value.Boolean)
	default:
		return string(value.Type)
	}
}

func predicateKey(predicate Predicate) string {
	encoded, _ := json.Marshal(predicate)
	return string(encoded)
}

func validateRequest(request Request) error {
	if request.ObjectTypeRef == "" {
		return invalid("object_type_ref is empty")
	}
	if request.Caller.UserID == "" {
		return invalid("caller user_id is empty")
	}
	for field, ids := range map[string][]string{
		"role_ids":              request.Caller.RoleIDs,
		"direct_department_ids": request.Caller.DirectDepartmentIDs,
		"department_tree_ids":   request.Caller.DepartmentTreeIDs,
	} {
		seen := make(map[string]struct{}, len(ids))
		for _, id := range ids {
			if id == "" {
				return invalid("caller %s contains an empty id", field)
			}
			if _, duplicate := seen[id]; duplicate {
				return invalid("caller %s contains duplicate id %q", field, id)
			}
			seen[id] = struct{}{}
		}
	}
	return nil
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidPlan, fmt.Sprintf(format, args...))
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
