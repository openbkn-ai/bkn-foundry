// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.

package knowledge_network

import (
	"context"
	"fmt"
	"sort"

	"bkn-backend/interfaces"
)

// compensateDatasetDocuments only touches documents attempted by the failed
// import. The committed database snapshot supplies their previous values.
func (kns *knowledgeNetworkService) compensateDatasetDocuments(ctx context.Context,
	documentIDs []string, snapshot *interfaces.KN) error {
	attempted := make(map[string]struct{}, len(documentIDs))
	for _, id := range documentIDs {
		if id != "" {
			attempted[id] = struct{}{}
		}
	}
	ids := make([]string, 0, len(attempted))
	for id := range attempted {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if snapshot == nil {
		for _, id := range ids {
			if err := kns.vbs.DeleteDatasetDocumentByID(ctx, interfaces.BKN_DATASET_ID, id); err != nil {
				return fmt.Errorf("remove failed import index document %s: %w", id, err)
			}
		}
		return nil
	}
	committed := make(map[string]struct{}, len(ids))
	markCommitted := func(moduleType, resourceID string) {
		id := interfaces.GenerateConceptDocuemtnID(snapshot.KNID, moduleType, resourceID, snapshot.Branch)
		committed[id] = struct{}{}
	}
	markCommitted(interfaces.MODULE_TYPE_KN, snapshot.KNID)
	for _, group := range snapshot.ConceptGroups {
		markCommitted(interfaces.MODULE_TYPE_CONCEPT_GROUP, group.CGID)
	}
	for _, object := range snapshot.ObjectTypes {
		markCommitted(interfaces.MODULE_TYPE_OBJECT_TYPE, object.OTID)
	}
	for _, relation := range snapshot.RelationTypes {
		markCommitted(interfaces.MODULE_TYPE_RELATION_TYPE, relation.RTID)
	}
	for _, action := range snapshot.ActionTypes {
		markCommitted(interfaces.MODULE_TYPE_ACTION_TYPE, action.ATID)
	}
	for _, risk := range snapshot.RiskTypes {
		markCommitted(interfaces.MODULE_TYPE_RISK_TYPE, risk.RTID)
	}
	for _, metric := range snapshot.Metrics {
		markCommitted(interfaces.MODULE_TYPE_METRIC, metric.ID)
	}
	for _, id := range ids {
		if _, exists := committed[id]; exists {
			continue
		}
		if err := kns.vbs.DeleteDatasetDocumentByID(ctx, interfaces.BKN_DATASET_ID, id); err != nil {
			return fmt.Errorf("remove failed import index document %s: %w", id, err)
		}
	}
	contains := func(moduleType, resourceID string) bool {
		id := interfaces.GenerateConceptDocuemtnID(snapshot.KNID, moduleType, resourceID, snapshot.Branch)
		_, ok := attempted[id]
		return ok
	}
	var groups []*interfaces.ConceptGroup
	for _, group := range snapshot.ConceptGroups {
		if contains(interfaces.MODULE_TYPE_CONCEPT_GROUP, group.CGID) {
			groups = append(groups, group)
		}
	}
	if len(groups) > 0 {
		if err := kns.cgs.InsertDatasetDatas(ctx, groups); err != nil {
			return fmt.Errorf("restore concept group index: %w", err)
		}
	}
	var objects []*interfaces.ObjectType
	for _, object := range snapshot.ObjectTypes {
		if contains(interfaces.MODULE_TYPE_OBJECT_TYPE, object.OTID) {
			objects = append(objects, object)
		}
	}
	if len(objects) > 0 {
		if err := kns.ots.InsertDatasetData(ctx, objects); err != nil {
			return fmt.Errorf("restore object type index: %w", err)
		}
	}
	var relations []*interfaces.RelationType
	for _, relation := range snapshot.RelationTypes {
		if contains(interfaces.MODULE_TYPE_RELATION_TYPE, relation.RTID) {
			relations = append(relations, relation)
		}
	}
	if len(relations) > 0 {
		if err := kns.rts.InsertDatasetData(ctx, relations); err != nil {
			return fmt.Errorf("restore relation type index: %w", err)
		}
	}
	var actions []*interfaces.ActionType
	for _, action := range snapshot.ActionTypes {
		if contains(interfaces.MODULE_TYPE_ACTION_TYPE, action.ATID) {
			actions = append(actions, action)
		}
	}
	if len(actions) > 0 {
		if err := kns.ats.InsertDatasetData(ctx, actions); err != nil {
			return fmt.Errorf("restore action type index: %w", err)
		}
	}
	var risks []*interfaces.RiskType
	for _, risk := range snapshot.RiskTypes {
		if contains(interfaces.MODULE_TYPE_RISK_TYPE, risk.RTID) {
			risks = append(risks, risk)
		}
	}
	if len(risks) > 0 {
		if err := kns.riskTypeS.InsertDatasetData(ctx, risks); err != nil {
			return fmt.Errorf("restore risk type index: %w", err)
		}
	}
	var metrics []*interfaces.MetricDefinition
	for _, metric := range snapshot.Metrics {
		if contains(interfaces.MODULE_TYPE_METRIC, metric.ID) {
			metrics = append(metrics, metric)
		}
	}
	if len(metrics) > 0 {
		if err := kns.ms.InsertDatasetData(ctx, metrics); err != nil {
			return fmt.Errorf("restore metric index: %w", err)
		}
	}
	if contains(interfaces.MODULE_TYPE_KN, snapshot.KNID) {
		if err := kns.InsertDatasetData(ctx, snapshot); err != nil {
			return fmt.Errorf("restore knowledge network index: %w", err)
		}
	}
	return nil
}
