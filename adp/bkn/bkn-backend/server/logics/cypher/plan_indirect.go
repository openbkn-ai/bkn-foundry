// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// An indirect relation links its ends through a row of a backing resource:
// the source matches a backing row on the source mapping rules, and the same
// backing row matches the target on the target mapping rules. It is planned
// as one join over three tables -- left, backing, right -- which the
// generator writes as two SQL joins.
//
// The backing table is not a node. It has no object type, so no row policy or
// property level applies to it; reading it is authorized as the resource it
// is, which the relation type grants through query_data, under the caller or
// under the network's proxy.

// indirectLink is what a relationship variable over an indirect relation
// needs besides the join: the backing table and both rule sets, in the
// model's source-to-target terms.
type indirectLink struct {
	table       int
	sourceRules []interfaces.Mapping
	targetRules []interfaces.Mapping
}

// indirectMapping reads an indirect relation type's backing resource and rules.
func indirectMapping(relationType *interfaces.RelationType) (*interfaces.InDirectMapping, error) {
	var rules *interfaces.InDirectMapping
	switch value := relationType.MappingRules.(type) {
	case *interfaces.InDirectMapping:
		rules = value
	case interfaces.InDirectMapping:
		rules = &value
	}
	if rules == nil {
		return nil, fmt.Errorf("relation type %q has no backing mapping to join through", relationType.RTName)
	}
	backing := rules.BackingDataSource
	if backing == nil || strings.TrimSpace(backing.ID) == "" {
		return nil, fmt.Errorf("relation type %q has no backing resource bound", relationType.RTName)
	}
	if backing.Type != "" && backing.Type != interfaces.DATA_SOURCE_TYPE_RESOURCE {
		return nil, fmt.Errorf("relation type %q is backed by a %s, and only a resource can be joined through",
			relationType.RTName, backing.Type)
	}
	if len(rules.SourceMappingRules) == 0 || len(rules.TargetMappingRules) == 0 {
		return nil, fmt.Errorf("relation type %q has no key mapping to join on", relationType.RTName)
	}
	for _, rule := range append(append([]interfaces.Mapping(nil), rules.SourceMappingRules...), rules.TargetMappingRules...) {
		if rule.SourceProp.Name == "" || rule.TargetProp.Name == "" {
			return nil, fmt.Errorf("relation type %q has a key mapping with an empty side", relationType.RTName)
		}
	}
	return rules, nil
}

// addIndirectJoin plans one relationship over an indirect relation type.
func (p *planner) addIndirectJoin(edge EdgeRef, relationType *interfaces.RelationType, left, right int) error {
	rules, err := indirectMapping(relationType)
	if err != nil {
		return &PlanError{Pos: edge.Pos, Err: err}
	}

	var readForwards []bool
	for _, forwards := range p.readings(edge.Direction) {
		source, target := left, right
		if !forwards {
			source, target = right, left
		}
		if p.objectType[source].OTID == relationType.SourceObjectTypeID &&
			p.objectType[target].OTID == relationType.TargetObjectTypeID {
			readForwards = append(readForwards, forwards)
		}
	}
	switch len(readForwards) {
	case 0:
		return planErrorf(edge.Pos,
			"relation type %q goes from %q to %q, which does not connect %q and %q the way this pattern reads it",
			relationType.RTName, relationType.SourceObjectTypeID, relationType.TargetObjectTypeID,
			p.objectType[left].OTID, p.objectType[right].OTID)
	case 1:
	default:
		// Both readings would have to agree on one backing row, which two
		// independent joins cannot say; the author knows which way it runs.
		return planErrorf(edge.Pos,
			"relation type %q links %q to itself through a backing resource, so an undirected pattern over it is ambiguous; write its direction",
			relationType.RTName, relationType.SourceObjectTypeID)
	}
	forwards := readForwards[0]
	source, target := left, right
	if !forwards {
		source, target = right, left
	}

	backing := len(p.plan.Tables)
	p.plan.Tables = append(p.plan.Tables, PlanTable{
		Alias:      fmt.Sprintf("t%d", backing),
		ResourceID: strings.TrimSpace(rules.BackingDataSource.ID),
		Optional:   edge.Optional,
		Backing:    true,
	})
	p.objectType = append(p.objectType, nil)

	// sourceKeys pair a source column with a backing column, and targetKeys a
	// backing column with a target column. The plan keeps keys in pattern
	// order, so a reversed reading swaps which side each set is on.
	sourceKeys := make([]PlanJoinKey, 0, len(rules.SourceMappingRules))
	for _, rule := range rules.SourceMappingRules {
		column, err := p.schema.Column(p.objectType[source], rule.SourceProp.Name)
		if err != nil {
			return &PlanError{Pos: edge.Pos, Err: err}
		}
		sourceKeys = append(sourceKeys, PlanJoinKey{LeftColumn: column, RightColumn: rule.TargetProp.Name})
	}
	targetKeys := make([]PlanJoinKey, 0, len(rules.TargetMappingRules))
	for _, rule := range rules.TargetMappingRules {
		column, err := p.schema.Column(p.objectType[target], rule.TargetProp.Name)
		if err != nil {
			return &PlanError{Pos: edge.Pos, Err: err}
		}
		targetKeys = append(targetKeys, PlanJoinKey{LeftColumn: rule.SourceProp.Name, RightColumn: column})
	}
	link := &PlanBacking{Table: backing, LeftKeys: sourceKeys, RightKeys: targetKeys}
	if !forwards {
		link.LeftKeys = swapKeys(targetKeys)
		link.RightKeys = swapKeys(sourceKeys)
	}

	join := PlanJoin{
		Left: left, Right: right, RelationTypeID: relationType.RTID, Direction: edge.Direction,
		Optional: edge.Optional, OptionalTable: edge.OptionalNode, Variable: edge.Variable,
		Backing: link,
	}
	if edge.Direction != Undirected {
		p.hops = append(p.hops, plannedHop{
			relationType: relationType.RTID,
			source:       source,
			target:       target,
			clause:       edge.Clause,
			optional:     edge.Optional,
			pos:          edge.Pos,
		})
	}
	if edge.Variable != "" {
		if err := p.bindRelationship(edge, relationType, nil, readForwards, left, right); err != nil {
			return err
		}
		variable := p.relationshipOf[edge.Variable]
		variable.indirect = &indirectLink{
			table: backing, sourceRules: rules.SourceMappingRules, targetRules: rules.TargetMappingRules,
		}
		variable.digest = indirectRelationDigest(relationType, rules)
		p.relationshipOf[edge.Variable] = variable
	}
	for _, condition := range edge.Conditions {
		planned, err := p.planPredicate(condition)
		if err != nil {
			return err
		}
		join.Conditions = append(join.Conditions, planned)
	}
	p.plan.Joins = append(p.plan.Joins, join)
	// Only the SQL path reads a backing resource.
	p.plan.SQLOnly = true
	return nil
}

// swapKeys turns key pairs around, for a reading that runs against the
// relation's direction.
func swapKeys(keys []PlanJoinKey) []PlanJoinKey {
	swapped := make([]PlanJoinKey, len(keys))
	for i, key := range keys {
		swapped[i] = PlanJoinKey{LeftColumn: key.RightColumn, RightColumn: key.LeftColumn}
	}
	return swapped
}

// indirectRelationDigest is relationDigest for an indirect binding: the
// backing resource and both rule sets name it.
func indirectRelationDigest(relationType *interfaces.RelationType, rules *interfaces.InDirectMapping) string {
	var b strings.Builder
	b.WriteString(relationType.RTID)
	b.WriteString("|backing=" + rules.BackingDataSource.ID)
	for _, rule := range rules.SourceMappingRules {
		b.WriteString("|source:" + rule.SourceProp.Name + "=" + rule.TargetProp.Name)
	}
	for _, rule := range rules.TargetMappingRules {
		b.WriteString("|target:" + rule.SourceProp.Name + "=" + rule.TargetProp.Name)
	}
	b.WriteString("|" + strconv.FormatInt(relationType.UpdateTime, 10))
	sum := sha256.Sum256([]byte(b.String()))
	return "sha256:" + hex.EncodeToString(sum[:])
}
