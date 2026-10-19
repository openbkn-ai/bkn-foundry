// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authz

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"gorm.io/gorm"
)

// Registry owns one startup snapshot. Before initialization, startup reconciliation
// can read the store through its transaction. After initialization, all readers
// (including policy transactions) share the same immutable definitions. Refresh
// requires a new Registry, normally a process restart.
//
// A failed initialization blocks reads until an explicit successful retry; there
// is no request-time fallback to the database after initialization was attempted.
type Registry struct {
	db    *gorm.DB
	state *registryState
}

type registryState struct {
	sync.RWMutex
	snapshot *registrySnapshot
	err      error
}

type registrySnapshot struct {
	types         []model.ResourceType
	operations    []model.Operation
	byType        map[string][]model.Operation
	typeByID      map[string]model.ResourceType
	operationByID map[[2]string]model.Operation
	direct        map[string]map[string][]string
	requirements  map[string]map[string][]string
	reverse       map[string]map[string][]string
	parents       map[string]map[string]string
	derived       map[string][]derivedRule
}

func newRegistry(db *gorm.DB) *Registry { return &Registry{db: db, state: &registryState{}} }

// Registry returns the shared static metadata reader. Boot must call
// InitializeRegistry before serving; constructors alone permit startup seeding.
func (en *Enforcer) Registry() *Registry {
	if en == nil {
		return newRegistry(nil)
	}
	if en.registry == nil {
		return newRegistry(en.db)
	}
	return en.registry
}

func (tx *PolicyTransaction) Registry() *Registry { return tx.enforcer.Registry() }

func (en *Enforcer) InitializeRegistry(ctx context.Context) error {
	return en.Registry().Initialize(ctx)
}

func (r *Registry) withDB(db *gorm.DB) *Registry { return &Registry{db: db, state: r.state} }

func (r *Registry) current() (*registrySnapshot, error) {
	r.state.RLock()
	defer r.state.RUnlock()
	if r.state.snapshot == nil && r.state.err == nil && r.db == nil {
		return nil, fmt.Errorf("authorization registry is not configured")
	}
	return r.state.snapshot, r.state.err
}

// Initialize loads and validates both tables before publishing any definitions.
// Repeated successful calls are no-ops, even if the persisted rows have changed.
func (r *Registry) Initialize(ctx context.Context) error {
	r.state.Lock()
	defer r.state.Unlock()
	if r.state.snapshot != nil {
		return nil
	}
	if r.db == nil {
		r.state.err = fmt.Errorf("authorization registry is not configured")
		return r.state.err
	}
	var types []model.ResourceType
	var ops []model.Operation
	err := r.db.WithContext(ctx).Transaction(func(db *gorm.DB) error {
		if err := db.Order("id").Find(&types).Error; err != nil {
			return err
		}
		return db.Order("resource_type_id, id").Find(&ops).Error
	})
	var snapshot *registrySnapshot
	if err == nil {
		if len(types) == 0 {
			err = fmt.Errorf("authorization registry has no resource types")
		} else {
			snapshot, err = buildRegistrySnapshot(types, ops)
		}
	}
	if err != nil {
		r.state.err = fmt.Errorf("initialize authorization registry: %w", err)
		return r.state.err
	}
	r.state.snapshot, r.state.err = snapshot, nil
	return nil
}

func copyOperation(op model.Operation) model.Operation {
	if op.Grantable != nil {
		value := *op.Grantable
		op.Grantable = &value
	}
	return op
}
func copyOperations(ops []model.Operation) []model.Operation {
	out := make([]model.Operation, len(ops))
	for i, op := range ops {
		out[i] = copyOperation(op)
	}
	return out
}

func (r *Registry) ResourceTypes(ctx context.Context) ([]model.ResourceType, error) {
	s, err := r.current()
	if err != nil {
		return nil, err
	}
	if s != nil {
		return append([]model.ResourceType{}, s.types...), nil
	}
	var rows []model.ResourceType
	err = r.db.WithContext(ctx).Order("id").Find(&rows).Error
	return rows, err
}

func (r *Registry) ResourceType(ctx context.Context, id string) (model.ResourceType, error) {
	s, err := r.current()
	if err != nil {
		return model.ResourceType{}, err
	}
	if s != nil {
		row, ok := s.typeByID[id]
		if !ok {
			return model.ResourceType{}, gorm.ErrRecordNotFound
		}
		return row, nil
	}
	var row model.ResourceType
	err = r.db.WithContext(ctx).First(&row, "id = ?", id).Error
	return row, err
}

// Operations returns a sorted copy. An empty resource type selects all types.
func (r *Registry) Operations(ctx context.Context, resourceType string) ([]model.Operation, error) {
	s, err := r.current()
	if err != nil {
		return nil, err
	}
	if s != nil {
		if resourceType == "" {
			return copyOperations(s.operations), nil
		}
		return copyOperations(s.byType[resourceType]), nil
	}
	var rows []model.Operation
	db := r.db.WithContext(ctx)
	if resourceType != "" {
		db = db.Where("resource_type_id = ?", resourceType)
	}
	err = db.Order("resource_type_id, id").Find(&rows).Error
	return rows, err
}

func (r *Registry) Operation(ctx context.Context, resourceType, id string) (model.Operation, error) {
	snapshot, err := r.current()
	if err != nil {
		return model.Operation{}, err
	}
	if snapshot != nil {
		op, ok := snapshot.operationByID[[2]string{resourceType, id}]
		if !ok {
			return model.Operation{}, gorm.ErrRecordNotFound
		}
		return copyOperation(op), nil
	}
	ops, err := r.Operations(ctx, resourceType)
	if err != nil {
		return model.Operation{}, err
	}
	for _, op := range ops {
		if op.ID == id {
			return op, nil
		}
	}
	return model.Operation{}, gorm.ErrRecordNotFound
}

func registryGraphs(ops []model.Operation) (map[string][]string, map[string][]string) {
	direct, reverse := map[string][]string{}, map[string][]string{}
	for _, op := range ops {
		direct[op.ID] = splitOperationIDs(op.RequiredOperationIDs)
		for _, dependency := range direct[op.ID] {
			reverse[dependency] = append(reverse[dependency], op.ID)
		}
	}
	return direct, reverse
}

// Requirements returns copies of the precomputed transitive prerequisites.
func (r *Registry) Requirements(ctx context.Context, resourceType string, operations []string) (map[string][]string, error) {
	return r.requirements(ctx, resourceType, operations, true, false)
}

func (r *Registry) requirements(ctx context.Context, resourceType string, operations []string, transitive, reverse bool) (map[string][]string, error) {
	s, err := r.current()
	if err != nil {
		return nil, err
	}
	result := make(map[string][]string, len(operations))
	if s != nil {
		byOperation := s.direct[resourceType]
		if transitive {
			byOperation = s.requirements[resourceType]
		}
		if reverse {
			byOperation = s.reverse[resourceType]
		}
		for _, op := range operations {
			if _, exists := byOperation[op]; (!transitive && !reverse && !exists) || (reverse && len(byOperation[op]) == 0) {
				continue
			}
			result[op] = append([]string(nil), byOperation[op]...)
		}
		return result, nil
	}
	ops, err := r.Operations(ctx, resourceType)
	if err != nil {
		return nil, err
	}
	graph, reversed := registryGraphs(ops)
	if reverse {
		graph = reversed
	}
	for _, op := range operations {
		if _, exists := graph[op]; !transitive && !reverse && !exists {
			continue
		}
		value := graph[op]
		if transitive || reverse {
			value = ExpandRequirements(graph, op)
		}
		if reverse {
			sort.Strings(value)
			if len(value) == 0 {
				continue
			}
		}
		result[op] = append([]string(nil), value...)
	}
	return result, nil
}

func (r *Registry) parentOperations(ctx context.Context, resourceType string) (map[string]string, error) {
	s, err := r.current()
	if err != nil {
		return nil, err
	}
	result := map[string]string{}
	if s != nil {
		for op, parent := range s.parents[resourceType] {
			result[op] = parent
		}
		return result, nil
	}
	ops, err := r.Operations(ctx, resourceType)
	if err != nil {
		return nil, err
	}
	for _, op := range ops {
		if op.ParentOperationID != "" {
			result[op.ID] = op.ParentOperationID
		}
	}
	return result, nil
}

func (r *Registry) derivedRules(ctx context.Context) (map[string][]derivedRule, error) {
	s, err := r.current()
	if err != nil {
		return nil, err
	}
	if s != nil {
		return s.derived, nil
	} // internal immutable map; never exposed to callers
	types, err := r.ResourceTypes(ctx)
	if err != nil {
		return nil, err
	}
	ops, err := r.Operations(ctx, "")
	if err != nil {
		return nil, err
	}
	return registryDerivations(types, ops), nil
}

func registryDerivations(types []model.ResourceType, ops []model.Operation) map[string][]derivedRule {
	parents := map[string]string{}
	for _, rt := range types {
		parents[rt.ID] = rt.ParentTypeID
	}
	result := map[string][]derivedRule{}
	for _, op := range ops {
		if parent := parents[op.ResourceTypeID]; parent != "" && op.DerivedToOperationID != "" {
			result[parent] = append(result[parent], derivedRule{childType: op.ResourceTypeID, childOp: op.ID, parentOp: op.DerivedToOperationID})
		}
	}
	return result
}

func buildRegistrySnapshot(types []model.ResourceType, ops []model.Operation) (*registrySnapshot, error) {
	if err := ValidateRegistryRequirements(types, ops); err != nil {
		return nil, err
	}
	if err := ValidateRegistryHierarchy(types, ops); err != nil {
		return nil, err
	}
	if err := ValidateRegistryDerivations(types, ops); err != nil {
		return nil, err
	}
	s := &registrySnapshot{types: append([]model.ResourceType{}, types...), operations: copyOperations(ops),
		byType: map[string][]model.Operation{}, typeByID: map[string]model.ResourceType{},
		operationByID: map[[2]string]model.Operation{}, direct: map[string]map[string][]string{},
		requirements: map[string]map[string][]string{}, reverse: map[string]map[string][]string{}, parents: map[string]map[string]string{}}
	sort.Slice(s.types, func(i, j int) bool { return s.types[i].ID < s.types[j].ID })
	sort.Slice(s.operations, func(i, j int) bool {
		a, b := s.operations[i], s.operations[j]
		if a.ResourceTypeID != b.ResourceTypeID {
			return a.ResourceTypeID < b.ResourceTypeID
		}
		return a.ID < b.ID
	})
	for _, rt := range s.types {
		s.typeByID[rt.ID] = rt
	}
	for _, op := range s.operations {
		s.byType[op.ResourceTypeID] = append(s.byType[op.ResourceTypeID], op)
		s.operationByID[[2]string{op.ResourceTypeID, op.ID}] = op
	}
	for _, rt := range s.types {
		direct, reverse := registryGraphs(s.byType[rt.ID])
		s.direct[rt.ID] = direct
		s.requirements[rt.ID], s.reverse[rt.ID], s.parents[rt.ID] = map[string][]string{}, map[string][]string{}, map[string]string{}
		for _, op := range s.byType[rt.ID] {
			s.requirements[rt.ID][op.ID] = ExpandRequirements(direct, op.ID)
			reversed := ExpandRequirements(reverse, op.ID)
			sort.Strings(reversed)
			s.reverse[rt.ID][op.ID] = reversed
			if op.ParentOperationID != "" {
				s.parents[rt.ID][op.ID] = op.ParentOperationID
			}
		}
	}
	s.derived = registryDerivations(s.types, s.operations)
	return s, nil
}

func registryDefinitions(types []model.ResourceType, ops []model.Operation) (map[string]model.ResourceType, map[string]map[string]model.Operation, error) {
	byType, byOp := map[string]model.ResourceType{}, map[string]map[string]model.Operation{}
	for _, rt := range types {
		if _, duplicate := byType[rt.ID]; duplicate || rt.ID == "" {
			return nil, nil, fmt.Errorf("empty or duplicate resource type %q", rt.ID)
		}
		byType[rt.ID], byOp[rt.ID] = rt, map[string]model.Operation{}
	}
	for _, op := range ops {
		operations, ok := byOp[op.ResourceTypeID]
		if !ok {
			return nil, nil, fmt.Errorf("operation %s/%s has unknown resource type", op.ResourceTypeID, op.ID)
		}
		if _, duplicate := operations[op.ID]; duplicate || op.ID == "" {
			return nil, nil, fmt.Errorf("empty or duplicate operation %s/%s", op.ResourceTypeID, op.ID)
		}
		operations[op.ID] = op
	}
	return byType, byOp, nil
}

// ValidateRegistryRequirements is shared by embedded seed validation and persisted startup loading.
func ValidateRegistryRequirements(types []model.ResourceType, ops []model.Operation) error {
	_, byOp, err := registryDefinitions(types, ops)
	if err != nil {
		return err
	}
	for _, rt := range types {
		visiting, done := map[string]bool{}, map[string]bool{}
		var visit func(string) error
		visit = func(id string) error {
			if visiting[id] {
				return fmt.Errorf("operation %s/%s has a requirement cycle", rt.ID, id)
			}
			if done[id] {
				return nil
			}
			visiting[id] = true
			for _, required := range splitOperationIDs(byOp[rt.ID][id].RequiredOperationIDs) {
				if required == id {
					return fmt.Errorf("operation %s/%s requires itself", rt.ID, id)
				}
				if _, ok := byOp[rt.ID][required]; !ok {
					return fmt.Errorf("operation %s/%s requires %q, which its resource type does not declare", rt.ID, id, required)
				}
				if err := visit(required); err != nil {
					return err
				}
			}
			visiting[id], done[id] = false, true
			return nil
		}
		for _, op := range ops {
			if op.ResourceTypeID == rt.ID {
				if err := visit(op.ID); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// ValidateRegistryHierarchy checks the declared type graph and explicit parent mappings.
func ValidateRegistryHierarchy(types []model.ResourceType, ops []model.Operation) error {
	byType, byOp, err := registryDefinitions(types, ops)
	if err != nil {
		return err
	}
	for _, rt := range types {
		if rt.ParentTypeID != "" {
			if _, ok := byType[rt.ParentTypeID]; !ok {
				return fmt.Errorf("resource type %q declares unknown parent_type %q", rt.ID, rt.ParentTypeID)
			}
			if rt.ID == rt.ParentTypeID {
				return fmt.Errorf("resource type %q is its own parent_type", rt.ID)
			}
		}
		seen := map[string]bool{rt.ID: true}
		for parent := rt.ParentTypeID; parent != ""; parent = byType[parent].ParentTypeID {
			if seen[parent] {
				return fmt.Errorf("resource type parent chain has a cycle at %q", parent)
			}
			seen[parent] = true
		}
	}
	for _, op := range ops {
		if op.ParentOperationID == "" {
			continue
		}
		parent := byType[op.ResourceTypeID].ParentTypeID
		if parent == "" {
			return fmt.Errorf("operation %s/%s declares parent_operation but has no parent_type", op.ResourceTypeID, op.ID)
		}
		if _, ok := byOp[parent][op.ParentOperationID]; !ok {
			return fmt.Errorf("operation %s/%s maps to %s/%s, which is not a registered operation", op.ResourceTypeID, op.ID, parent, op.ParentOperationID)
		}
	}
	return nil
}

// ValidateRegistryDerivations rejects missing, grantable, or recursively derived targets.
func ValidateRegistryDerivations(types []model.ResourceType, ops []model.Operation) error {
	byType, byOp, err := registryDefinitions(types, ops)
	if err != nil {
		return err
	}
	for _, op := range ops {
		if op.DerivedToOperationID == "" {
			continue
		}
		parent := byType[op.ResourceTypeID].ParentTypeID
		if parent == "" {
			return fmt.Errorf("operation %s/%s declares derived_to_operation but has no parent_type", op.ResourceTypeID, op.ID)
		}
		target, ok := byOp[parent][op.DerivedToOperationID]
		if !ok {
			return fmt.Errorf("operation %s/%s derives to %s/%s, which is not a registered operation", op.ResourceTypeID, op.ID, parent, op.DerivedToOperationID)
		}
		if target.IsGrantable() {
			return fmt.Errorf("operation %s/%s derives to grantable operation %s/%s", op.ResourceTypeID, op.ID, parent, target.ID)
		}
		if target.DerivedToOperationID != "" {
			return fmt.Errorf("operation %s/%s derives to %s/%s, which derives again", op.ResourceTypeID, op.ID, parent, target.ID)
		}
	}
	return nil
}
