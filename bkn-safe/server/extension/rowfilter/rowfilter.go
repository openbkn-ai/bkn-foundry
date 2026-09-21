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
	"log/slog"
	"regexp"
	"sort"
	"sync/atomic"

	"github.com/openbkn-ai/licverify"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/permobject"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
)

// Capability deliberately reuses the existing Enterprise authorization
// bundle. It is a display name, not a new licence feature key.
const Capability = permobject.Capability

const (
	// MaxObjectTypesPerRequest bounds one trusted batch decision. It deliberately
	// matches the existing property-level object batch bound so one ontology
	// query can obtain both decisions without introducing another fan-out shape.
	MaxObjectTypesPerRequest = 100
	maxObjectIDLength        = 40
)

var objectTypeRefPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}/[a-z0-9][a-z0-9_-]{0,39}$`)
var propertyNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,39}$`)

// ErrInvalidPlan means an extension returned a plan outside the deliberately
// small, safe predicate language. Callers must fail closed and never compile
// a partial or arbitrary expression.
var ErrInvalidPlan = errors.New("invalid row filter plan")

// ErrResolverUnavailable means an Enterprise or Industry process is running
// without a healthy EE implementation. It is intentionally distinct from the
// documented low-edition TRUE fallback: treating an assembly failure as a
// downgrade would silently expose rows.
var ErrResolverUnavailable = errors.New("row filter resolver unavailable")

// ErrLifecycleUnavailable means a paid process cannot clean policy references
// as part of a resource delete. Delete callers must fail closed rather than
// leave a policy that could revive when the same id is recreated.
var ErrLifecycleUnavailable = errors.New("row filter lifecycle unavailable")

// Caller is the server-derived identity context available to policy
// resolution. Roles and department ranges are populated only by bkn-safe's
// trusted directory/authz resolver; request payloads must never supply them.
type Caller struct {
	UserID              string
	RoleIDs             []string
	DirectDepartmentIDs []string
	DepartmentTreeIDs   []string
}

// Request is a batch of object-type row-filter decisions for one trusted
// caller. Role and department expansion happens once per request, never once
// for every object type in a traversal.
type Request struct {
	ObjectTypeRefs []string
	Caller         Caller
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

// Decision is one normalized row-filter result in a batch response.
type Decision struct {
	ObjectTypeRef string
	Plan          Plan
}

// Resolution is the complete extension-owned result. Core validates that it
// contains exactly one decision for every requested object reference before it
// exposes the response to a query executor.
type Resolution struct {
	Decisions []Decision
}

// ResponseEntry is a decision plus its core-computed digest.
type ResponseEntry struct {
	ObjectTypeRef            string
	Plan                     Plan
	EffectiveRowFilterDigest string
}

// Response is the complete, validated result handed to execution code.
type Response struct {
	Entries []ResponseEntry
}

// Resolver is implemented only by the Enterprise code line. Resolve must be
// safe for concurrent use and return every requested object type in one batch.
// It must not re-resolve Caller or issue per-object policy lookups.
type Resolver interface {
	Resolve(ctx context.Context, request Request) (Resolution, error)
}

// SubjectType identifies a persisted policy subject.
type SubjectType string

const (
	SubjectTypeUser SubjectType = "user"
	SubjectTypeRole SubjectType = "role"
)

// Lifecycle is the optional EE-side policy cleanup hook. bkn-safe passes its
// active database transaction, allowing the EE policy rows to be removed in
// the same transaction as a user or role delete. Object-type and knowledge
// network callers use the same stable hook from their own lifecycle contract.
// Community builds leave it unregistered and therefore use a no-op cleanup.
type Lifecycle interface {
	DeleteSubject(ctx context.Context, tx *gorm.DB, subjectType SubjectType, subjectID string) error
	DeleteObjectType(ctx context.Context, objectTypeRef string) error
	DeleteKnowledgeNetwork(ctx context.Context, knowledgeNetworkID string) error
}

var implementation atomic.Value          // Resolver
var lifecycleImplementation atomic.Value // Lifecycle
var resolverUnavailableReported atomic.Bool

// Register installs the Enterprise resolver during process assembly. It is
// unconditional with respect to the live certificate; Resolve evaluates the
// tier on every call so activation and downgrade need no restart.
func Register(min licverify.Edition, resolver Resolver) {
	if resolver == nil {
		panic("rowfilter: Register(nil)")
	}
	if min != licverify.EditionEnterprise {
		panic("rowfilter: minimum edition must be Enterprise")
	}
	if load() != nil {
		panic("rowfilter: resolver already registered")
	}
	entitlement.MustBeAssembling("rowfilter")
	entitlement.MarkAssembled(Capability, min)
	implementation.Store(resolver)
}

func Registered() bool { return load() != nil }

func Available() bool {
	return load() != nil && entitlement.AtLeast(licverify.EditionEnterprise)
}

// ReadinessError makes an active Enterprise/Industry assembly failure visible
// to orchestration before a query reaches the decision endpoint. Community and
// low-edition deployments deliberately remain ready without an EE resolver.
func ReadinessError() error {
	if entitlement.AtLeast(licverify.EditionEnterprise) && load() == nil {
		reportResolverUnavailable()
		return ErrResolverUnavailable
	}
	return nil
}

// Resolve returns the documented TRUE fallback only when the trusted license
// is below Enterprise. An Enterprise/Industry process with no resolver is an
// assembly fault and fails closed.
func Resolve(ctx context.Context, request Request) (Response, error) {
	if err := validateRequest(request); err != nil {
		return Response{}, err
	}
	if !entitlement.AtLeast(licverify.EditionEnterprise) {
		return Fallback(request)
	}
	resolver := load()
	if resolver == nil {
		reportResolverUnavailable()
		return Response{}, ErrResolverUnavailable
	}
	resolution, err := resolver.Resolve(ctx, request)
	if err != nil {
		return Response{}, err
	}
	return responseFor(request, resolution)
}

// Fallback is the documented Community/no-license compatibility behavior: no
// policy exists at this layer, so the effective row filter is TRUE. It never
// bypasses the separate object-type query_data authorization check.
func Fallback(request Request) (Response, error) {
	if err := validateRequest(request); err != nil {
		return Response{}, err
	}
	decisions := make([]Decision, 0, len(request.ObjectTypeRefs))
	for _, objectTypeRef := range request.ObjectTypeRefs {
		decisions = append(decisions, Decision{ObjectTypeRef: objectTypeRef, Plan: Plan{Predicate: Predicate{Kind: PredicateTrue}}})
	}
	return responseFor(request, Resolution{Decisions: decisions})
}

func responseFor(request Request, resolution Resolution) (Response, error) {
	byObjectTypeRef := make(map[string]Plan, len(resolution.Decisions))
	for _, decision := range resolution.Decisions {
		if !ValidObjectTypeRef(decision.ObjectTypeRef) {
			return Response{}, invalid("resolver returned invalid object_type_ref %q", decision.ObjectTypeRef)
		}
		if _, duplicate := byObjectTypeRef[decision.ObjectTypeRef]; duplicate {
			return Response{}, invalid("resolver returned duplicate object_type_ref %q", decision.ObjectTypeRef)
		}
		byObjectTypeRef[decision.ObjectTypeRef] = decision.Plan
	}
	entries := make([]ResponseEntry, 0, len(request.ObjectTypeRefs))
	for _, objectTypeRef := range request.ObjectTypeRefs {
		plan, present := byObjectTypeRef[objectTypeRef]
		if !present {
			return Response{}, invalid("resolver omitted object_type_ref %q", objectTypeRef)
		}
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
		entries = append(entries, ResponseEntry{
			ObjectTypeRef:            objectTypeRef,
			Plan:                     normalized,
			EffectiveRowFilterDigest: "sha256:" + hex.EncodeToString(digest[:]),
		})
		delete(byObjectTypeRef, objectTypeRef)
	}
	if len(byObjectTypeRef) != 0 {
		return Response{}, invalid("resolver returned object_type_ref outside the request")
	}
	return Response{Entries: entries}, nil
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
		if !ValidPropertyName(predicate.Property) || len(predicate.Predicates) != 0 {
			return Predicate{}, invalid("in predicate must have one property and no child predicates")
		}
		if len(predicate.Values) == 0 {
			return Predicate{}, invalid("in predicate must contain values")
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
	if len(request.ObjectTypeRefs) == 0 || len(request.ObjectTypeRefs) > MaxObjectTypesPerRequest {
		return invalid("object_type_refs must contain 1 to %d entries", MaxObjectTypesPerRequest)
	}
	seenObjects := make(map[string]struct{}, len(request.ObjectTypeRefs))
	for _, objectTypeRef := range request.ObjectTypeRefs {
		if !ValidObjectTypeRef(objectTypeRef) {
			return invalid("object_type_ref is invalid")
		}
		if _, duplicate := seenObjects[objectTypeRef]; duplicate {
			return invalid("object_type_refs contains duplicate object_type_ref %q", objectTypeRef)
		}
		seenObjects[objectTypeRef] = struct{}{}
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

// ValidObjectTypeRef is the canonical Safe sub-resource reference validator.
// It is intentionally shared with property levels and row filtering so these
// two authorization contracts cannot diverge in accepted identifier shape.
func ValidObjectTypeRef(reference string) bool {
	return len(reference) <= maxObjectIDLength*2+1 && objectTypeRefPattern.MatchString(reference)
}

// ValidPropertyName is shared by the property-level and row-filter contracts.
// A predicate may name only a published DataProperty identifier, never an
// arbitrary backend field or expression fragment.
func ValidPropertyName(name string) bool {
	return propertyNamePattern.MatchString(name)
}

// RegisterLifecycle installs the Enterprise policy lifecycle cleaner. It is
// separate from Resolver so lifecycle cleanup remains available during a
// trusted license downgrade, preventing stale policies from reviving later.
func RegisterLifecycle(lifecycle Lifecycle) {
	if lifecycle == nil {
		panic("rowfilter: RegisterLifecycle(nil)")
	}
	if loadLifecycle() != nil {
		panic("rowfilter: lifecycle already registered")
	}
	entitlement.MustBeAssembling("rowfilter")
	lifecycleImplementation.Store(lifecycle)
}

func DeleteSubject(ctx context.Context, tx *gorm.DB, subjectType SubjectType, subjectID string) error {
	if (subjectType != SubjectTypeUser && subjectType != SubjectTypeRole) || subjectID == "" || tx == nil {
		return ErrLifecycleUnavailable
	}
	lifecycle := loadLifecycle()
	if lifecycle == nil {
		if entitlement.AtLeast(licverify.EditionEnterprise) {
			return ErrLifecycleUnavailable
		}
		return nil
	}
	return lifecycle.DeleteSubject(ctx, tx, subjectType, subjectID)
}

func DeleteObjectType(ctx context.Context, objectTypeRef string) error {
	if !ValidObjectTypeRef(objectTypeRef) {
		return ErrLifecycleUnavailable
	}
	lifecycle := loadLifecycle()
	if lifecycle == nil {
		if entitlement.AtLeast(licverify.EditionEnterprise) {
			return ErrLifecycleUnavailable
		}
		return nil
	}
	return lifecycle.DeleteObjectType(ctx, objectTypeRef)
}

func DeleteKnowledgeNetwork(ctx context.Context, knowledgeNetworkID string) error {
	if knowledgeNetworkID == "" {
		return ErrLifecycleUnavailable
	}
	lifecycle := loadLifecycle()
	if lifecycle == nil {
		if entitlement.AtLeast(licverify.EditionEnterprise) {
			return ErrLifecycleUnavailable
		}
		return nil
	}
	return lifecycle.DeleteKnowledgeNetwork(ctx, knowledgeNetworkID)
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

func loadLifecycle() Lifecycle {
	value := lifecycleImplementation.Load()
	if value == nil {
		return nil
	}
	lifecycle, _ := value.(Lifecycle)
	return lifecycle
}

func reportResolverUnavailable() {
	if resolverUnavailableReported.CompareAndSwap(false, true) {
		slog.Error("row-filter Enterprise resolver unavailable; denying row-filter decisions and failing readiness", "capability", Capability)
	}
}

func reset() {
	implementation = atomic.Value{}
	lifecycleImplementation = atomic.Value{}
	resolverUnavailableReported = atomic.Bool{}
	resetManagement()
}
