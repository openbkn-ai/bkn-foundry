// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package batchindex builds request-scoped BatchIDIndex values from KN / concept group payloads.
package batchindex

import (
	"encoding/json"
	"fmt"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// NewBatchIDIndex constructs an empty index; empty branch defaults to MAIN_BRANCH.
func NewBatchIDIndex(knID, branch string) *interfaces.BatchIDIndex {
	if branch == "" {
		branch = interfaces.MAIN_BRANCH
	}
	return &interfaces.BatchIDIndex{
		KNID:            knID,
		Branch:          branch,
		ObjectTypes:     make(map[string]*interfaces.ObjectType),
		RelationTypes:   make(map[string]*interfaces.RelationType),
		RelationTypeIDs: make(map[string]struct{}),
		ActionTypeIDs:   make(map[string]struct{}),
		ConceptGroupIDs: make(map[string]struct{}),
	}
}

// EnsureObjectTypePropertyMap fills PropertyMap from DataProperties when unset, for mapping rule validation.
func EnsureObjectTypePropertyMap(ot *interfaces.ObjectType) {
	if ot == nil {
		return
	}
	if len(ot.PropertyMap) > 0 {
		return
	}
	if len(ot.DataProperties) == 0 {
		return
	}
	ot.PropertyMap = make(map[string]string, len(ot.DataProperties))
	for _, prop := range ot.DataProperties {
		if prop == nil {
			continue
		}
		ot.PropertyMap[prop.Name] = prop.DisplayName
	}
}

// MergeBatchIndex merges src into dst (mutates dst). Returns an error when the same object type ID has conflicting payloads.
func MergeBatchIndex(dst, src *interfaces.BatchIDIndex) error {
	if src == nil || dst == nil {
		return nil
	}
	for id, ot := range src.ObjectTypes {
		if err := addObjectType(dst, id, ot); err != nil {
			return err
		}
	}
	if dst.RelationTypes == nil {
		dst.RelationTypes = make(map[string]*interfaces.RelationType)
	}
	for _, rt := range src.RelationTypes {
		if err := ingestRelationType(dst, rt); err != nil {
			return err
		}
	}
	for id := range src.RelationTypeIDs {
		dst.RelationTypeIDs[id] = struct{}{}
	}
	for id := range src.ActionTypeIDs {
		dst.ActionTypeIDs[id] = struct{}{}
	}
	for id := range src.ConceptGroupIDs {
		dst.ConceptGroupIDs[id] = struct{}{}
	}
	return nil
}

// CollectKNFromPayload collects a BatchIDIndex from a full KN (including nested concept group buckets).
func CollectKNFromPayload(kn *interfaces.KN) (*interfaces.BatchIDIndex, error) {
	if kn == nil {
		return NewBatchIDIndex("", interfaces.MAIN_BRANCH), nil
	}
	branch := kn.Branch
	if branch == "" {
		branch = interfaces.MAIN_BRANCH
	}
	idx := NewBatchIDIndex(kn.KNID, branch)
	for _, ot := range kn.ObjectTypes {
		if err := ingestObjectType(idx, ot); err != nil {
			return nil, err
		}
	}
	for _, rt := range kn.RelationTypes {
		if err := ingestRelationType(idx, rt); err != nil {
			return nil, err
		}
	}
	for _, at := range kn.ActionTypes {
		ingestActionType(idx, at)
	}
	for _, cg := range kn.ConceptGroups {
		if err := ingestConceptGroup(idx, cg); err != nil {
			return nil, err
		}
	}
	return idx, nil
}

// CollectFromConceptGroups collects an index from concept groups only (may nest object/relation/action).
func CollectFromConceptGroups(knID, branch string, groups []*interfaces.ConceptGroup) (*interfaces.BatchIDIndex, error) {
	if branch == "" {
		branch = interfaces.MAIN_BRANCH
	}
	idx := NewBatchIDIndex(knID, branch)
	for _, cg := range groups {
		if err := ingestConceptGroup(idx, cg); err != nil {
			return nil, err
		}
	}
	return idx, nil
}

func ingestConceptGroup(b *interfaces.BatchIDIndex, cg *interfaces.ConceptGroup) error {
	if cg == nil {
		return nil
	}
	if cg.CGID != "" {
		b.ConceptGroupIDs[cg.CGID] = struct{}{}
	}
	for _, ot := range cg.ObjectTypes {
		if err := ingestObjectType(b, ot); err != nil {
			return err
		}
	}
	for _, rt := range cg.RelationTypes {
		if err := ingestRelationType(b, rt); err != nil {
			return err
		}
	}
	for _, at := range cg.ActionTypes {
		ingestActionType(b, at)
	}
	return nil
}

func ingestObjectType(b *interfaces.BatchIDIndex, ot *interfaces.ObjectType) error {
	if ot == nil {
		return nil
	}
	if err := addObjectType(b, ot.OTID, ot); err != nil {
		return err
	}
	for _, cg := range ot.ConceptGroups {
		if cg != nil && cg.CGID != "" {
			b.ConceptGroupIDs[cg.CGID] = struct{}{}
		}
	}
	return nil
}

func ingestRelationType(b *interfaces.BatchIDIndex, rt *interfaces.RelationType) error {
	if rt == nil || rt.RTID == "" {
		return nil
	}
	if b.RelationTypes == nil {
		b.RelationTypes = make(map[string]*interfaces.RelationType)
	}
	if existing := b.RelationTypes[rt.RTID]; existing != nil {
		left, err1 := json.Marshal(existing.RelationTypeWithKeyField)
		right, err2 := json.Marshal(rt.RelationTypeWithKeyField)
		if err1 != nil || err2 != nil || string(left) != string(right) {
			return fmt.Errorf("conflicting relation type definitions for id %q", rt.RTID)
		}
	}
	b.RelationTypes[rt.RTID] = rt
	b.RelationTypeIDs[rt.RTID] = struct{}{}
	return nil
}

func ingestActionType(b *interfaces.BatchIDIndex, at *interfaces.ActionType) {
	if at == nil || at.ATID == "" {
		return
	}
	b.ActionTypeIDs[at.ATID] = struct{}{}
}

func addObjectType(b *interfaces.BatchIDIndex, id string, ot *interfaces.ObjectType) error {
	if id == "" || ot == nil {
		return nil
	}
	if existing, ok := b.ObjectTypes[id]; ok {
		if !objectTypeBatchPayloadEqual(existing, ot) {
			return fmt.Errorf("conflicting object type definitions for id %q", id)
		}
		return nil
	}
	b.ObjectTypes[id] = ot
	return nil
}

func objectTypeBatchPayloadEqual(a, b *interfaces.ObjectType) bool {
	if a == nil || b == nil {
		return a == b
	}
	aj, e1 := json.Marshal(a.ObjectTypeWithKeyField)
	bj, e2 := json.Marshal(b.ObjectTypeWithKeyField)
	return e1 == nil && e2 == nil && string(aj) == string(bj)
}

// HasObjectTypeID reports whether the batch declares the object type ID (including nested payloads).
func HasObjectTypeID(id string, b *interfaces.BatchIDIndex) bool {
	if b == nil || id == "" {
		return false
	}
	_, ok := b.ObjectTypes[id]
	return ok
}

// HasConceptGroupID reports whether the batch declares the concept group ID.
func HasConceptGroupID(id string, b *interfaces.BatchIDIndex) bool {
	if b == nil || id == "" {
		return false
	}
	_, ok := b.ConceptGroupIDs[id]
	return ok
}
