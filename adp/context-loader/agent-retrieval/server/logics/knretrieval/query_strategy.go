// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knretrieval

import (
	"context"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

const (
	// dataSampleLimit number of data samples.
	dataSampleLimit = 2
)

// parallelExecSemanticQueryStrategy execution recall strategy (concurrency)
func (k *knRetrievalServiceImpl) parallelExecSemanticQueryStrategy(ctx context.Context,
	knID string, strategys []*interfaces.SemanticQueryStrategy,
) ([]*interfaces.ConceptResult, error) {
	var wg sync.WaitGroup
	resultChan := make(chan []*interfaces.ConceptResult, len(strategys))
	errChan := make(chan error, len(strategys))

	for _, strategy := range strategys {
		wg.Add(1)
		go func(s *interfaces.SemanticQueryStrategy) {
			defer wg.Done()
			res, err := k.execSemanticQueryStrategy(ctx, knID, s)
			if err != nil {
				errChan <- err
				return
			}
			resultChan <- res
		}(strategy)
	}

	wg.Wait()
	close(resultChan)
	close(errChan)

	var allResults []*interfaces.ConceptResult
	for res := range resultChan {
		allResults = append(allResults, res...)
	}

	var errors []error
	for err := range errChan {
		errors = append(errors, err)
	}

	if len(allResults) == 0 && len(errors) > 0 {
		return nil, errors[0]
	}

	if len(errors) > 0 {
		k.logger.Error("semantic query strategies execution errors",
			"error_count", len(errors),
			"first_error", errors[0].Error())
	}
	return allResults, nil
}

// execSemanticQueryStrategy executes the recall strategy: different strategy template execution --- single strategy execution.
func (k *knRetrievalServiceImpl) execSemanticQueryStrategy(ctx context.Context,
	knID string, strategy *interfaces.SemanticQueryStrategy,
) (result []*interfaces.ConceptResult, err error) {
	switch strategy.StrategyType {
	case interfaces.ConceptDiscoveryStrategy: // concept discovery.
		return k.execConceptDiscoveryStrategy(ctx, knID, strategy)
	case interfaces.ObjectInstanceDiscoveryStrategy: // Object instance lookup.
		return k.execObjectInstanceDiscoveryStrategy(ctx, knID, strategy)
	case interfaces.ConceptGetStrategy: // concept acquisition.
		return k.execConceptGetStrategy(ctx, knID, strategy)
	}
	return
}

func (k *knRetrievalServiceImpl) execObjectInstanceDiscoveryStrategy(ctx context.Context,
	knID string, strategy *interfaces.SemanticQueryStrategy,
) (conceptResults []*interfaces.ConceptResult, err error) {
	if strategy.Filter == nil || strategy.Filter.ConceptID == "" {
		return
	}
	req := &interfaces.QueryObjectInstancesReq{
		KnID:               knID,
		OtID:               strategy.Filter.ConceptID,
		IncludeTypeInfo:    true,
		IncludeLogicParams: true,
		Limit:              dataSampleLimit,
	}
	// todo: condition conversion to be implemented.

	resp, err := k.ontologyQueryAccess.QueryObjectInstances(ctx, req)
	if err != nil {
		return nil, err
	}

	conceptResults = []*interfaces.ConceptResult{}
	if resp != nil && resp.ObjectConcept != nil {
		conceptResult := interfaces.ConceptResult{
			ConceptType:   interfaces.KnConceptTypeObject,
			ConceptDetail: resp.ObjectConcept,
			Samples:       resp.Data,
		}
		if id, ok := resp.ObjectConcept[string(interfaces.ConceptFieldID)].(string); ok {
			conceptResult.ConceptID = id
		}
		if name, ok := resp.ObjectConcept[string(interfaces.ConceptFieldName)].(string); ok {
			conceptResult.ConceptName = name
		}
		conceptResults = append(conceptResults, &conceptResult)
	}
	return
}

// execConceptGetStrategy concept acquisition strategy.
func (k *knRetrievalServiceImpl) execConceptGetStrategy(ctx context.Context,
	knID string, strategy *interfaces.SemanticQueryStrategy,
) (conceptResults []*interfaces.ConceptResult, err error) {
	if strategy.Filter == nil {
		return
	}
	filter := strategy.Filter
	var ConceptIDs []string
	if filter.ConceptID != "" {
		ConceptIDs = append(ConceptIDs, filter.ConceptID)
	}

	if len(filter.ConceptIDs) > 0 {
		ConceptIDs = append(ConceptIDs, filter.ConceptIDs...)
	}

	if len(ConceptIDs) == 0 {
		return
	}

	conceptDetailsMap := map[interfaces.KnConceptType][]any{}
	switch filter.ConceptType {
	case interfaces.KnConceptTypeObject:
		var objectDetails []*interfaces.ObjectType
		objectDetails, err = k.bknBackendAccess.GetObjectTypeDetail(ctx, knID, ConceptIDs, true)
		if err != nil {
			k.logger.WithContext(ctx).Errorf("[execConceptGetStrategy] execConceptGetStrategy failed. knId:%s, objectConceptIDs:%v\n",
				knID, ConceptIDs)
			return
		}
		conceptDetailsMap[interfaces.KnConceptTypeObject] = append(conceptDetailsMap[interfaces.KnConceptTypeObject], objectDetails)
	case interfaces.KnConceptTypeRelation:
		var relationDetails []*interfaces.RelationType
		relationDetails, err = k.bknBackendAccess.GetRelationTypeDetail(ctx, knID, ConceptIDs, true)
		if err != nil {
			k.logger.WithContext(ctx).Errorf("[execConceptGetStrategy] execConceptGetStrategy failed. knId:%s, relationConceptIDs:%v\n",
				knID, ConceptIDs)
			return
		}
		conceptDetailsMap[interfaces.KnConceptTypeObject] = append(conceptDetailsMap[interfaces.KnConceptTypeObject], relationDetails)
	case interfaces.KnConceptTypeAction:
		var actionDetails []*interfaces.ActionType
		actionDetails, err = k.bknBackendAccess.GetActionTypeDetail(ctx, knID, ConceptIDs, true)
		if err != nil {
			k.logger.WithContext(ctx).Errorf("[execConceptGetStrategy] execConceptGetStrategy failed. knId:%s, actionConceptIDs:%v\n",
				knID, ConceptIDs)
			return
		}
		conceptDetailsMap[interfaces.KnConceptTypeObject] = append(conceptDetailsMap[interfaces.KnConceptTypeObject], actionDetails)
	}

	if err != nil {
		k.logger.Errorf("[execConceptGetStrategy] getDetail failed. knId:%s, ConceptIDs:%v\n",
			knID, ConceptIDs)
		return
	}

	if len(conceptDetailsMap) == 0 {
		return
	}

	conceptResults = []*interfaces.ConceptResult{}
	for conceptType, conceptDetails := range conceptDetailsMap {
		if len(conceptDetails) == 0 {
			continue
		}
		for _, conceptDetail := range conceptDetails {
			conceptResult := &interfaces.ConceptResult{
				ConceptType:   conceptType,
				ConceptDetail: conceptDetail,
			}
			switch conceptType {
			case interfaces.KnConceptTypeObject:
				concept := conceptDetail.(*interfaces.ObjectType)
				conceptResult.ConceptID = concept.ID
				conceptResult.ConceptName = concept.Name
				conceptResult.MatchScore = interfaces.MaxMatchScore
			case interfaces.KnConceptTypeAction:
				concept := conceptDetail.(*interfaces.ActionType)
				conceptResult.ConceptID = concept.ID
				conceptResult.ConceptName = concept.Name
				conceptResult.MatchScore = interfaces.MaxMatchScore
			case interfaces.KnConceptTypeRelation:
				concept := conceptDetail.(*interfaces.RelationType)
				conceptResult.ConceptID = concept.ID
				conceptResult.ConceptName = concept.Name
				conceptResult.MatchScore = interfaces.MaxMatchScore
			}
			conceptResults = append(conceptResults, conceptResult)
		}
	}
	return
}

// execConceptDiscoveryStrategy executes concept discovery strategy.
func (k *knRetrievalServiceImpl) execConceptDiscoveryStrategy(ctx context.Context,
	knID string, strategy *interfaces.SemanticQueryStrategy,
) (conceptResults []*interfaces.ConceptResult, err error) {
	if strategy.Filter == nil {
		return
	}
	filter := strategy.Filter
	if len(filter.Conditions) == 0 {
		return
	}
	conceptSearchConfig := config.NewConfigLoader().ConceptSearchConfig
	var subCond []*interfaces.KnCondition
	for _, fCond := range filter.Conditions {
		var operationType interfaces.KnOperationType
		operationType, err = ParseKnOperationType(fCond.Operation)
		if err != nil {
			k.logger.Warnf("[execConceptDiscoveryStrategy],ParseKnOperationType faild, strategy operation: %v", fCond.Operation)
			continue
		}
		knCond := &interfaces.KnCondition{
			Field:     fCond.Field,
			Operation: operationType,
			Value:     fCond.Value,
			ValueFrom: interfaces.CondValueFromConst,
		}

		if operationType == interfaces.KnOperationTypeKnn {
			knCond.Value = fCond.Value
			knCond.LimitKey = interfaces.CondLimitKeyK
			knCond.LimitValue = conceptSearchConfig.KnnKValue
		}

		subCond = append(subCond, knCond)
	}

	if len(subCond) == 0 {
		k.logger.Warnf("[execConceptDiscoveryStrategy], parse condition is empty, strategy: %v", strategy)
		return
	}

	cond := &interfaces.KnCondition{
		Operation:     interfaces.KnOperationTypeOr,
		SubConditions: subCond,
	}

	queryConceptsReq := &interfaces.QueryConceptsReq{
		KnID:  knID,
		Cond:  cond,
		Limit: conceptSearchConfig.ConceptRecallSize,
	}

	switch filter.ConceptType {
	case interfaces.KnConceptTypeObject:
		conceptResults, err = k.discoveryObjectConcepts(ctx, queryConceptsReq)
	case interfaces.KnConceptTypeRelation:
		conceptResults, err = k.discoveryRelationTypeConcepts(ctx, queryConceptsReq)
	case interfaces.KnConceptTypeAction:
		conceptResults, err = k.discoveryActionTypeConcepts(ctx, queryConceptsReq)
	}

	return
}

// discoveryObjectConcepts discovery object type concept.
func (k *knRetrievalServiceImpl) discoveryObjectConcepts(ctx context.Context,
	queryConceptsReq *interfaces.QueryConceptsReq,
) (conceptResults []*interfaces.ConceptResult, err error) {
	var objectTypes *interfaces.ObjectTypeConcepts
	objectTypes, err = k.bknBackendAccess.SearchObjectTypes(ctx, queryConceptsReq)
	if err != nil {
		k.logger.Errorf("[discoveryObjectConcepts] SearchObjectTypes failed, userId: %s, visitorType: %s, req: %v", queryConceptsReq)
		return
	}

	if objectTypes == nil {
		return
	}

	if len(objectTypes.Entries) == 0 {
		return
	}

	conceptResults = []*interfaces.ConceptResult{}
	for _, entry := range objectTypes.Entries {
		conceptResult := interfaces.ConceptResult{
			ConceptType:   interfaces.KnConceptTypeObject,
			ConceptDetail: entry,
		}
		conceptResult.ConceptID = entry.ID
		conceptResult.ConceptName = entry.Name
		conceptResult.MatchScore = entry.Score
		conceptResults = append(conceptResults, &conceptResult)
	}
	return
}

// discoveryRelationTypeConcepts discovery relation type concept.
func (k *knRetrievalServiceImpl) discoveryRelationTypeConcepts(ctx context.Context,
	queryConceptsReq *interfaces.QueryConceptsReq,
) (conceptResults []*interfaces.ConceptResult, err error) {
	var relationTypes *interfaces.RelationTypeConcepts
	relationTypes, err = k.bknBackendAccess.SearchRelationTypes(ctx, queryConceptsReq)
	if err != nil {
		k.logger.Errorf("[discoveryObjectConcepts] SearchRelationTypes failed, userId: %s, visitorType: %s, req: %v", queryConceptsReq)
		return
	}

	if relationTypes == nil {
		return
	}

	if len(relationTypes.Entries) == 0 {
		return
	}

	conceptResults = []*interfaces.ConceptResult{}
	for _, entry := range relationTypes.Entries {
		conceptResult := interfaces.ConceptResult{
			ConceptType:   interfaces.KnConceptTypeRelation,
			ConceptDetail: entry,
		}
		conceptResult.ConceptID = entry.ID
		conceptResult.ConceptName = entry.Name
		conceptResult.MatchScore = entry.Score
		conceptResults = append(conceptResults, &conceptResult)
	}
	return
}

// discoveryActionTypeConcepts discovery action class concepts.
func (k *knRetrievalServiceImpl) discoveryActionTypeConcepts(ctx context.Context,
	queryConceptsReq *interfaces.QueryConceptsReq,
) (conceptResults []*interfaces.ConceptResult, err error) {
	var actionTypes *interfaces.ActionTypeConcepts
	actionTypes, err = k.bknBackendAccess.SearchActionTypes(ctx, queryConceptsReq)
	if err != nil {
		k.logger.Errorf("[discoveryActionTypeConcepts] SearchActionTypes failed, userId: %s, visitorType: %s, req: %v", queryConceptsReq)
		return
	}

	if actionTypes == nil {
		return
	}

	if len(actionTypes.Entries) == 0 {
		return
	}

	conceptResults = []*interfaces.ConceptResult{}
	for _, entry := range actionTypes.Entries {
		conceptResult := interfaces.ConceptResult{
			ConceptType:   interfaces.KnConceptTypeAction,
			ConceptDetail: entry,
		}
		conceptResult.ConceptID = entry.ID
		conceptResult.ConceptName = entry.Name
		conceptResult.MatchScore = entry.Score
		conceptResults = append(conceptResults, &conceptResult)
	}
	return
}

// buildConceptDiscoveryStrategy Build concept discovery query strategy.
func (k *knRetrievalServiceImpl) buildConceptDiscoveryStrategy(conceptType interfaces.KnConceptType,
	query string, otherConds []*interfaces.QueryStrategyCondition,
) (queryStrategy *interfaces.SemanticQueryStrategy) {
	conds := []*interfaces.QueryStrategyCondition{}
	// Build a query strategy based on the fragments segmented by the original Query.
	if query != "" {
		// matchCondition keyword matching condition.
		matchCondition := &interfaces.QueryStrategyCondition{
			Field:     string(interfaces.ConceptFieldAny),
			Operation: string(interfaces.KnOperationTypeMatch),
			Value:     query,
		}
		conds = append(conds, matchCondition)

		// KnnCondition vectorretrievecondition.
		knnCondition := &interfaces.QueryStrategyCondition{
			Field:     string(interfaces.ConceptFieldAny),
			Operation: string(interfaces.KnOperationTypeKnn),
			Value:     query,
		}
		conds = append(conds, knnCondition)
	}
	// otherConds other conditions.
	if len(otherConds) > 0 {
		conds = append(conds, otherConds...)
	}

	if len(conds) == 0 {
		return nil
	}

	// Building a concept discovery query strategy.
	discoveryStrategy := &interfaces.SemanticQueryStrategy{
		StrategyType: interfaces.ConceptDiscoveryStrategy,
		Filter: &interfaces.QueryStrategyFilter{
			ConceptType: conceptType,
		},
	}
	discoveryStrategy.Filter.Conditions = conds

	return discoveryStrategy
}
