// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knowledge_network

import (
	"context"
	"fmt"
	"net/http"
	"sort"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	cond "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common/condition"
	oerrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/logics"
)

const defaultFilteredCrossJoinMaxExpand = 10000

func (kns *knowledgeNetworkService) filteredCrossJoinMaxExpand() int {
	n := kns.appSetting.ServerSetting.FilteredCrossJoinMaxEdgeExpand
	if n <= 0 {
		return defaultFilteredCrossJoinMaxExpand
	}
	return n
}

// expandFilteredCrossJoin builds next-hop objects for relation type filtered_cross_join:
// Current-side batches use the backing resource for LIKE, whose collation may
// differ from Go, and use local evaluation for other conditions.
// Next-side instances: relation mapping condition AND path-node ActualCondition, via GetObjectsByObjectTypeID.
// Pairs are emitted in stable order (current ObjectID asc, then next row order) until maxEdgeExpand (silent truncate).
func (kns *knowledgeNetworkService) expandFilteredCrossJoin(ctx context.Context,
	query *interfaces.SubGraphQueryBaseOnSource,
	batch []interfaces.LevelObject,
	edge *interfaces.TypeEdge,
	nextTypeMeta interfaces.ObjectTypeWithKeyField,
	isForward bool,
	rules *interfaces.FilteredCrossJoinMapping,
) (map[string]interfaces.Objects, error) {

	maxQ := kns.filteredCrossJoinMaxExpand()
	var filterCurrent *cond.CondCfg
	var queryNext *cond.CondCfg
	if isForward {
		filterCurrent = rules.SourceCondition
		queryNext = rules.TargetCondition
	} else {
		filterCurrent = rules.TargetCondition
		queryNext = rules.SourceCondition
	}

	filtered, err := kns.filterLevelObjectsByCondition(ctx, query.KNID, query.Branch,
		query.IgnoreLocalIndex, batch, filterCurrent)
	if err != nil {
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("filtered_cross_join evaluate current instance: %s", err.Error()))
	}
	if len(filtered) == 0 {
		return nil, nil
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].ObjectID < filtered[j].ObjectID
	})

	var nextObjectTypeID string
	if isForward {
		nextObjectTypeID = edge.RelationType.TargetObjectTypeID
	} else {
		nextObjectTypeID = edge.RelationType.SourceObjectTypeID
	}

	nextObjectQuery := &interfaces.ObjectQueryBaseOnObjectType{
		KNID:         query.KNID,
		Branch:       query.Branch,
		ObjectTypeID: nextObjectTypeID,
		PageQuery: interfaces.PageQuery{
			NeedTotal: false,
			Limit:     maxQ,
		},
		CommonQueryParameters: interfaces.CommonQueryParameters{
			IncludeTypeInfo:    true,
			IncludeLogicParams: query.IncludeLogicParams,
			IgnoreLocalIndex:   query.IgnoreLocalIndex,
		},
	}

	// Next hop: FCJ mapping condition AND path object-type condition (e.g. A→B→C with filter on C).
	nextObjectQuery.ActualCondition = queryNext
	if nextTypeMeta.ActualCondition != nil {
		nextObjectQuery.ActualCondition = &cond.CondCfg{
			Operation: cond.OperationAnd,
			SubConds:  []*cond.CondCfg{nextObjectQuery.ActualCondition, nextTypeMeta.ActualCondition},
		}
	}

	fullNextOT := interfaces.ObjectType{ObjectTypeWithKeyField: nextTypeMeta}
	nextObjectQuery.Sort = logics.BuildViewSort(fullNextOT)

	nextObjects, err := kns.ots.GetObjectsByObjectTypeID(ctx, nextObjectQuery)
	if err != nil {
		return nil, err
	}

	result := make(map[string]interfaces.Objects)
	pairCount := 0
	for _, s := range filtered {
		if pairCount >= maxQ {
			break
		}
		for _, trow := range nextObjects.Datas {
			if pairCount >= maxQ {
				break
			}
			objs := result[s.ObjectID]
			if objs.Datas == nil {
				objs = interfaces.Objects{
					Datas:      []map[string]any{},
					ObjectType: nextObjects.ObjectType,
					TotalCount: 0,
				}
			}
			objs.Datas = append(objs.Datas, trow)
			objs.TotalCount++
			result[s.ObjectID] = objs
			pairCount++
		}
	}

	return result, nil
}

// matchFilteredCrossJoinRelations pairs source and target instances using filtered_cross_join rules with the same
// ordering and silent truncation as expandFilteredCrossJoin (for SearchSubgraphByObjects).
func (kns *knowledgeNetworkService) matchFilteredCrossJoinRelations(ctx context.Context,
	query *interfaces.SubGraphQueryBaseOnObjects,
	sourceObjects []interfaces.LevelObject,
	targetObjects []interfaces.LevelObject,
	edge *interfaces.TypeEdge,
) ([]interfaces.Relation, error) {

	rules, ok := edge.RelationType.MappingRules.(*interfaces.FilteredCrossJoinMapping)
	if !ok {
		return nil, nil
	}

	maxQ := kns.filteredCrossJoinMaxExpand()
	src, err := kns.filterLevelObjectsByCondition(ctx, query.KNID, query.Branch,
		query.IgnoreLocalIndex, sourceObjects, rules.SourceCondition)
	if err != nil {
		return nil, err
	}
	tgt, err := kns.filterLevelObjectsByCondition(ctx, query.KNID, query.Branch,
		query.IgnoreLocalIndex, targetObjects, rules.TargetCondition)
	if err != nil {
		return nil, err
	}
	if len(src) == 0 || len(tgt) == 0 {
		return nil, nil
	}

	relations := make([]interfaces.Relation, 0)
	pairCount := 0
	for _, s := range src {
		if pairCount >= maxQ {
			break
		}
		for _, t := range tgt {
			if pairCount >= maxQ {
				break
			}
			relations = append(relations, interfaces.Relation{
				RelationTypeId:   edge.RelationTypeId,
				RelationTypeName: edge.RelationType.RTName,
				SourceObjectId:   s.ObjectID,
				TargetObjectId:   t.ObjectID,
			})
			pairCount++
		}
	}
	return relations, nil
}

// filterLevelObjectsByCondition delegates LIKE evaluation to the backing resource,
// whose collation may differ from Go's case-sensitive string matching.
func (kns *knowledgeNetworkService) filterLevelObjectsByCondition(ctx context.Context,
	knID, branch string, ignoreLocalIndex bool, objs []interfaces.LevelObject, c *cond.CondCfg) ([]interfaces.LevelObject, error) {
	out := make([]interfaces.LevelObject, 0, len(objs))
	if cond.ContainsLikeCondition(c) {
		byType := make(map[string][]interfaces.LevelObject)
		for _, lo := range objs {
			if lo.ObjectType != nil {
				byType[lo.ObjectType.OTID] = append(byType[lo.ObjectType.OTID], lo)
			}
		}
		matched := make(map[string]bool)
		for typeID, group := range byType {
			for start := 0; start < len(group); start += 50 {
				end := min(start+50, len(group))
				identities := make([]map[string]any, 0, end-start)
				for _, lo := range group[start:end] {
					identity := lo.ObjectUK
					if len(identity) == 0 {
						return nil, fmt.Errorf("instance %q has no identity for LIKE source filtering", lo.ObjectID)
					}
					identities = append(identities, identity)
				}
				objects, err := kns.ots.ScanObjectsByObjectTypeID(ctx, &interfaces.ObjectQueryBaseOnObjectType{
					KNID: knID, Branch: branch, ObjectTypeID: typeID,
					ActualCondition: &cond.CondCfg{Operation: cond.OperationAnd, SubConds: []*cond.CondCfg{
						logics.BuildInstanceIdentitiesCondition(identities), c,
					}},
					CommonQueryParameters: interfaces.CommonQueryParameters{IgnoreLocalIndex: ignoreLocalIndex},
				}, end-start)
				if err != nil {
					return nil, err
				}
				if objects.Incomplete || len(objects.Datas) > end-start {
					return nil, fmt.Errorf("incomplete LIKE source filtering for object type %q", typeID)
				}
				for _, row := range objects.Datas {
					id, ok := row[interfaces.SYSTEM_PROPERTY_INSTANCE_ID].(string)
					if !ok || id == "" {
						return nil, fmt.Errorf("LIKE source filtering returned an instance without an ID")
					}
					matched[id] = true
				}
			}
		}
		for _, lo := range objs {
			if matched[lo.ObjectID] {
				out = append(out, lo)
			}
		}
	} else {
		for _, lo := range objs {
			if lo.ObjectType == nil {
				continue
			}
			ok, err := logics.EvaluateInstanceAgainstCondition(ctx, lo.ObjectData, c, lo.ObjectType)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			out = append(out, lo)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ObjectID < out[j].ObjectID })
	return out, nil
}
