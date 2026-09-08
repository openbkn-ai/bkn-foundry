// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledge_network

import (
	"context"
	"net/http"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	oerrors "ontology-query/errors"
	"ontology-query/interfaces"
	propertyaccess "ontology-query/logics/property_access"
)

type relationPropertyRequirements map[string]map[string]struct{}

func (kns *knowledgeNetworkService) requireFullPathInputs(ctx context.Context, knID, branch string,
	paths []interfaces.RelationTypePath) error {
	objectTypes := map[string]*interfaces.ObjectType{}
	pathObjectTypes := map[string]interfaces.ObjectTypeWithKeyField{}
	requirements := relationPropertyRequirements{}
	for _, path := range paths {
		for _, pathObjectType := range path.ObjectTypes {
			copy := interfaces.ObjectType{
				ObjectTypeWithKeyField: pathObjectType,
				KNID:                   knID,
				Branch:                 branch,
			}
			objectTypes[pathObjectType.OTID] = &copy
			pathObjectTypes[pathObjectType.OTID] = pathObjectType
		}
	}
	resolve := func(objectTypeID string) error {
		objectType := objectTypes[objectTypeID]
		if objectType != nil && len(objectType.DataProperties) > 0 {
			return nil
		}
		pathObjectType, exists := pathObjectTypes[objectTypeID]
		if !exists {
			pathObjectType = interfaces.ObjectTypeWithKeyField{OTID: objectTypeID}
		}
		resolved, found, err := kns.resolvePathObjectType(ctx, knID, branch, pathObjectType)
		if err != nil {
			return err
		}
		if !found {
			return rest.NewHTTPError(ctx, http.StatusNotFound,
				oerrors.OntologyQuery_ObjectType_ObjectTypeNotFound)
		}
		objectTypes[objectTypeID] = &resolved
		return nil
	}
	for _, path := range paths {
		for _, pathObjectType := range path.ObjectTypes {
			needsSchema := pathObjectType.ActualCondition != nil
			for _, sort := range pathObjectType.Sort {
				if sort != nil && sort.Field != interfaces.SORT_FIELD_SCORE {
					needsSchema = true
					break
				}
			}
			if needsSchema {
				if err := resolve(pathObjectType.OTID); err != nil {
					return err
				}
			}
			objectType := objectTypes[pathObjectType.OTID]
			propertyNames := dataPropertyNames(*objectType)
			addRelationFields(requirements, pathObjectType.OTID,
				propertyaccess.CollectConditionFields(pathObjectType.ActualCondition, propertyNames)...)
			for _, sort := range pathObjectType.Sort {
				if sort != nil && sort.Field != interfaces.SORT_FIELD_SCORE {
					addRelationFields(requirements, pathObjectType.OTID, sort.Field)
				}
			}
		}
		for _, edge := range path.TypeEdges {
			if _, ok := edge.RelationType.MappingRules.(*interfaces.FilteredCrossJoinMapping); ok {
				if err := resolve(edge.RelationType.SourceObjectTypeID); err != nil {
					return err
				}
				if err := resolve(edge.RelationType.TargetObjectTypeID); err != nil {
					return err
				}
			}
			addRelationMappingRequirements(requirements, edge.RelationType, objectTypes)
		}
	}
	for objectTypeID := range requirements {
		if err := resolve(objectTypeID); err != nil {
			return err
		}
	}
	return kns.requireFullRelationProperties(ctx, knID, objectTypes, requirements)
}

func (kns *knowledgeNetworkService) resolvePathObjectType(ctx context.Context, knID, branch string,
	pathObjectType interfaces.ObjectTypeWithKeyField) (interfaces.ObjectType, bool, error) {
	if len(pathObjectType.DataProperties) > 0 {
		return interfaces.ObjectType{
			ObjectTypeWithKeyField: pathObjectType,
			KNID:                   knID,
			Branch:                 branch,
		}, true, nil
	}
	if kns.omAccess == nil {
		return interfaces.ObjectType{}, false, rest.NewHTTPError(ctx, http.StatusServiceUnavailable,
			oerrors.OntologyQuery_ObjectType_InternalError_GetObjectTypesByIDFailed).
			WithErrorDetails("object type schema resolver is not configured")
	}
	objectType, exists, err := kns.omAccess.GetObjectType(ctx, knID, branch, pathObjectType.OTID)
	if err != nil {
		return interfaces.ObjectType{}, false, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			oerrors.OntologyQuery_ObjectType_InternalError_GetObjectTypesByIDFailed).
			WithErrorDetails("failed to load object type schema for relation access")
	}
	objectType.KNID = knID
	objectType.Branch = branch
	return objectType, exists, nil
}

func (kns *knowledgeNetworkService) requireFullRelationInputs(ctx context.Context, knID string,
	objectTypes map[string]*interfaces.ObjectType, relationTypes map[string]interfaces.RelationType) error {
	requirements := relationPropertyRequirements{}
	for _, relationType := range relationTypes {
		addRelationMappingRequirements(requirements, relationType, objectTypes)
	}
	return kns.requireFullRelationProperties(ctx, knID, objectTypes, requirements)
}

func (kns *knowledgeNetworkService) requireFullRelationProperties(ctx context.Context, knID string,
	objectTypes map[string]*interfaces.ObjectType, requirements relationPropertyRequirements) error {
	if len(requirements) == 0 {
		return nil
	}
	objects := make([]interfaces.ObjectType, 0, len(objectTypes))
	for _, objectType := range objectTypes {
		if objectType == nil {
			continue
		}
		copy := *objectType
		copy.KNID = knID
		objects = append(objects, copy)
	}
	plan, err := propertyaccess.Build(ctx, kns.propertyAccess, objects)
	if err != nil {
		return err
	}
	for objectTypeID, fields := range requirements {
		names := make([]string, 0, len(fields))
		for name := range fields {
			names = append(names, name)
		}
		if !plan.AllFull(propertyaccess.ObjectTypeRef(knID, objectTypeID), names) {
			return rest.NewHTTPError(ctx, http.StatusBadRequest,
				oerrors.OntologyQuery_KnowledgeNetwork_InvalidParameter).
				WithErrorDetails("the relation depends on a property unavailable for this operation")
		}
	}
	return nil
}

func addRelationMappingRequirements(requirements relationPropertyRequirements, relationType interfaces.RelationType,
	objectTypes map[string]*interfaces.ObjectType) {
	switch rules := relationType.MappingRules.(type) {
	case []interfaces.Mapping:
		for _, mapping := range rules {
			addRelationFields(requirements, relationType.SourceObjectTypeID, mapping.SourceProp.Name)
			addRelationFields(requirements, relationType.TargetObjectTypeID, mapping.TargetProp.Name)
		}
	case *interfaces.InDirectMapping:
		if rules == nil {
			return
		}
		for _, mapping := range rules.SourceMappingRules {
			addRelationFields(requirements, relationType.SourceObjectTypeID, mapping.SourceProp.Name)
		}
		for _, mapping := range rules.TargetMappingRules {
			addRelationFields(requirements, relationType.TargetObjectTypeID, mapping.TargetProp.Name)
		}
	case interfaces.InDirectMapping:
		for _, mapping := range rules.SourceMappingRules {
			addRelationFields(requirements, relationType.SourceObjectTypeID, mapping.SourceProp.Name)
		}
		for _, mapping := range rules.TargetMappingRules {
			addRelationFields(requirements, relationType.TargetObjectTypeID, mapping.TargetProp.Name)
		}
	case *interfaces.FilteredCrossJoinMapping:
		if rules == nil {
			return
		}
		var sourceProperties, targetProperties []string
		if source := objectTypes[relationType.SourceObjectTypeID]; source != nil {
			sourceProperties = dataPropertyNames(*source)
		}
		if target := objectTypes[relationType.TargetObjectTypeID]; target != nil {
			targetProperties = dataPropertyNames(*target)
		}
		addRelationFields(requirements, relationType.SourceObjectTypeID,
			propertyaccess.CollectConditionFields(rules.SourceCondition, sourceProperties)...)
		addRelationFields(requirements, relationType.TargetObjectTypeID,
			propertyaccess.CollectConditionFields(rules.TargetCondition, targetProperties)...)
	}
}

func addRelationFields(requirements relationPropertyRequirements, objectTypeID string, fields ...string) {
	objectTypeID = strings.TrimSpace(objectTypeID)
	if objectTypeID == "" {
		return
	}
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		if requirements[objectTypeID] == nil {
			requirements[objectTypeID] = map[string]struct{}{}
		}
		requirements[objectTypeID][field] = struct{}{}
	}
}

func dataPropertyNames(objectType interfaces.ObjectType) []string {
	result := make([]string, 0, len(objectType.DataProperties))
	for _, property := range objectType.DataProperties {
		result = append(result, property.Name)
	}
	return result
}
