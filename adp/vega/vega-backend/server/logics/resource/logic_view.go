// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package resource

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/dlclark/regexp2"
	"github.com/mitchellh/mapstructure"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"go.opentelemetry.io/otel/codes"

	verrors "vega-backend/errors"
	"vega-backend/interfaces"
	fcond "vega-backend/logics/filter_condition"
)

// Some general operations for creating and updating views
func (rs *resourceService) validateLogicDefinition(ctx context.Context, view *interfaces.ResourceRequest) (string, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "logic layer: Common operation for creating and updating views")
	defer span.End()

	// Custom View
	if view.LogicDefinition == nil {
		return "", rest.NewHTTPError(ctx, http.StatusBadRequest, rest.PublicError_BadRequest).
			WithErrorDetails("Logic definition is empty")
	}

	// Verify the uniqueness of the node ID
	nodeMap := make(map[string]struct{})
	for _, node := range view.LogicDefinition {
		if node.ID == "" {
			return "", rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
				WithErrorDetails("Node ID cannot be empty")
		}
		if _, exists := nodeMap[node.ID]; exists {
			return "", rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_Duplicated_NodeID).
				WithErrorDetails(fmt.Sprintf("Duplicate node ID found: %s", node.ID))
		}
		nodeMap[node.ID] = struct{}{}
	}

	resourceNodeCount := 0
	outputNodeCount := 0
	refResourceMap := make(map[string]*interfaces.Resource)

	for _, node := range view.LogicDefinition {
		// Nodes cannot self-reference
		if slices.Contains(node.Inputs, node.ID) {
			return "", rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
				WithErrorDetails(fmt.Sprintf("Node '%s' cannot reference itself: %s", node.ID, node.ID))
		}

		switch node.Type {
		case interfaces.LogicDefinitionNodeType_Resource:
			// Verify the resource node
			err := validateResourceNode(ctx, rs, node, refResourceMap)
			if err != nil {
				return "", err
			}

			resourceNodeCount++
		case interfaces.LogicDefinitionNodeType_Join:
			err := validateJoinNode(ctx, rs, node, view.LogicDefinition, nodeMap)
			if err != nil {
				return "", err
			}
		case interfaces.LogicDefinitionNodeType_Union:
			err := validateUnionNode(ctx, rs, view.Category, node, view.LogicDefinition, nodeMap)
			if err != nil {
				return "", err
			}
		case interfaces.LogicDefinitionNodeType_Sql:
			err := validateSqlNode(ctx, rs, node, view.LogicDefinition, nodeMap)
			if err != nil {
				return "", err
			}
		case interfaces.LogicDefinitionNodeType_Output:
			err := validateOutputNode(ctx, rs, node, view.LogicDefinition, nodeMap)
			if err != nil {
				return "", err
			}

			outputNodeCount++

		default:
			return "", rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
				WithErrorDetails("The logic definition node type is invalid")
		}
	}

	// Determine the view type: derivative view or composite view
	logicType := determineLogicType(view.LogicDefinition)

	var refResourceCategory string
	refResourceCategoryMap := make(map[string]struct{})
	refResourceCatalogMap := make(map[string]struct{})
	for _, dsView := range refResourceMap {
		refResourceCatalogMap[dsView.CatalogID] = struct{}{}
		refResourceCategoryMap[dsView.Category] = struct{}{}
		refResourceCategory = dsView.Category
	}

	if len(refResourceCategoryMap) != 1 {
		return "", rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
			WithErrorDetails("The source view of the custom view must have the same category")
	}

	// If the data source type is opensearch, cross-OpenSearch data source selection is not possible
	if refResourceCategory == interfaces.ResourceCategoryIndex && len(refResourceCatalogMap) > 1 {
		return "", rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
			WithErrorDetails("The source view of query type DSL must have the same data source when create custom view")
	}

	span.SetStatus(codes.Ok, "")
	return logicType, nil
}

// The determineLogicType determines whether the view type is a derived view or a combined view
// Derived view: The output node only references one resource node (without going through multi-source processing nodes such as Join/Union/SQL)
// Combined view: The output node references multiple resource nodes or has undergone processing nodes such as Join/Union/SQL
func determineLogicType(nodes []*interfaces.LogicDefinitionNode) string {
	// The default is the combined view
	logicType := interfaces.LogicType_Composite

	// Find the output node
	var outputNode *interfaces.LogicDefinitionNode
	for _, node := range nodes {
		if node.Type == interfaces.LogicDefinitionNodeType_Output {
			outputNode = node
			break
		}
	}

	if outputNode != nil && len(outputNode.Inputs) == 1 {
		// The output node has only one input. Check whether only one resource node is referenced
		// Walk input nodes recursively and check that they resolve to one resource without passing through Join, Union, or SQL nodes.
		hasProcessingNode := false
		resourceNodeIDs := make(map[string]struct{})

		// Traverse all upstream nodes using BFS
		visited := make(map[string]struct{})
		queue := []string{outputNode.Inputs[0]}
		visited[outputNode.Inputs[0]] = struct{}{}

		for len(queue) > 0 {
			currentID := queue[0]
			queue = queue[1:]

			// Find the current node
			var currentNode *interfaces.LogicDefinitionNode
			for _, n := range nodes {
				if n.ID == currentID {
					currentNode = n
					break
				}
			}

			if currentNode == nil {
				continue
			}

			// Check the node type
			switch currentNode.Type {
			case interfaces.LogicDefinitionNodeType_Resource:
				// Record resource node
				resourceNodeIDs[currentNode.ID] = struct{}{}
			case interfaces.LogicDefinitionNodeType_Join,
				interfaces.LogicDefinitionNodeType_Union,
				interfaces.LogicDefinitionNodeType_Sql:
				// When encountering a processing node, mark it as a composite view
				hasProcessingNode = true
			case interfaces.LogicDefinitionNodeType_Output:
				// It shouldn't have appeared, but was ignored
				// break
			}

			// Add the input node to the queue
			for _, inputID := range currentNode.Inputs {
				if _, ok := visited[inputID]; !ok {
					visited[inputID] = struct{}{}
					queue = append(queue, inputID)
				}
			}
		}

		// If there is only one resource node and no processing node, it is a derived view
		if !hasProcessingNode && len(resourceNodeIDs) == 1 {
			logicType = interfaces.LogicType_Derived
		}
	}

	return logicType
}

// Obtain the output field mapping of the node (used to verify whether the field exists)
func getNodeOutputFieldsMap(ctx context.Context, rs *resourceService, nodeID string,
	allNodes []*interfaces.LogicDefinitionNode, nodeCache map[string]map[string]*interfaces.Property) (map[string]*interfaces.Property, error) {

	// If it has already been calculated, return the cached result directly
	if cached, ok := nodeCache[nodeID]; ok {
		return cached, nil
	}

	// Find the node
	var node *interfaces.LogicDefinitionNode
	for _, n := range allNodes {
		if n.ID == nodeID {
			node = n
			break
		}
	}
	if node == nil {
		return nil, fmt.Errorf("node %s not found", nodeID)
	}

	fieldsMap := make(map[string]*interfaces.Property)

	switch node.Type {
	case interfaces.LogicDefinitionNodeType_Resource:
		// Resource node: Obtains the list of fields from the resource
		var cfg interfaces.ResourceNodeCfg
		if err := mapstructure.Decode(node.Config, &cfg); err != nil {
			return nil, err
		}
		resource, err := rs.GetByID(ctx, cfg.ResourceID)
		if err != nil {
			return nil, err
		}
		for _, field := range resource.SchemaDefinition {
			fieldsMap[field.Name] = field
		}
	default:
		// Other nodes: Obtain the list of fields from output_fields
		for _, field := range node.OutputFields {
			if field.Name == "*" {
				// Wildcard mode: All fields need to be obtained from the upstream node
				for _, inputID := range node.Inputs {
					// Recursively obtain the fields of the input node
					inputFieldsMap, err := getNodeOutputFieldsMap(ctx, rs, inputID, allNodes, nodeCache)
					if err != nil {
						return nil, err
					}
					for name, f := range inputFieldsMap {
						fieldsMap[name] = f
					}
				}
			} else {
				// Non-wildcards: Directly use field definitions
				prop := &interfaces.Property{
					Name:        field.Name,
					Type:        field.Type,
					DisplayName: field.DisplayName,
				}
				fieldsMap[field.Name] = prop
			}
		}
	}

	// Cache the result
	nodeCache[nodeID] = fieldsMap
	return fieldsMap, nil
}

func validateResourceNode(ctx context.Context, dvs *resourceService, node *interfaces.LogicDefinitionNode,
	refResourceMap map[string]*interfaces.Resource) error {
	// The input node of the resource node must be empty
	if len(node.Inputs) != 0 {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
			WithErrorDetails("The resource node must have no input node")
	}

	var cfg interfaces.ResourceNodeCfg
	err := mapstructure.Decode(node.Config, &cfg)
	if err != nil {
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, rest.PublicError_InternalServerError).
			WithErrorDetails(fmt.Sprintf("decode resource node config failed, %v", err))
	}

	// To determine whether the source table of the custom view exists, the field list can be obtained from this function
	atomicView, err := dvs.GetByID(ctx, cfg.ResourceID)
	if err != nil {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
			WithErrorDetails(fmt.Sprintf("get resource %s failed, %v", cfg.ResourceID, err))
	}

	// Verify the type of the source view
	switch atomicView.Category {
	case interfaces.ResourceCategoryTable:
	case interfaces.ResourceCategoryFile:
	case interfaces.ResourceCategoryFileset:
	case interfaces.ResourceCategoryAPI:
	case interfaces.ResourceCategoryTopic:
	case interfaces.ResourceCategoryIndex:
	default:
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
			WithErrorDetails(fmt.Sprintf("The source resource of the custom view '%s' is not supported", cfg.ResourceID))
	}

	refResourceMap[atomicView.ID] = atomicView

	// fieldsMap is the mapping of field name and fields
	fieldsMap := make(map[string]*interfaces.Property)
	for _, viewField := range atomicView.SchemaDefinition {
		fieldsMap[viewField.Name] = viewField
	}

	// Verify the filtering conditions
	httpErr := validateCond(ctx, cfg.Filters, fieldsMap)
	if httpErr != nil {
		return httpErr
	}

	// Verify deduplication configuration, only table deduplication configuration
	if cfg.Distinct {
		if atomicView.Category != interfaces.ResourceCategoryTable {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
				WithErrorDetails("The logic definition view category is not table, distinct config is not supported")
		}
	}

	// Verify the output field format: The resource node supports wildcard mode and projection mode
	for _, field := range node.OutputFields {
		// Wildcard mode: Only "*" is allowed
		if field.Name == "*" {
			// In wildcard mode, no other field configurations should be provided
			if field.Type != "" || field.From != "" || field.FromNode != "" || len(field.FromList) > 0 {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
					WithErrorDetails("Wildcard field '*' should not have additional configuration")
			}
			continue
		}

		// Projection mode: Only field names are allowed; there should be no mapping or alignment configurations
		if field.From != "" || field.FromNode != "" || len(field.FromList) > 0 {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
				WithErrorDetails(fmt.Sprintf("Resource node output field '%s' should not have from, from_node or from_list configuration", field.Name))
		}

		// Check whether the verification field exists in the list of resource fields
		if _, ok := fieldsMap[field.Name]; !ok {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, rest.PublicError_BadRequest).
				WithErrorDetails(fmt.Sprintf("The field '%s' is not in the view '%s' field list", field.Name, atomicView.Name))
		}
	}

	return nil
}

func validateJoinNode(ctx context.Context, rs *resourceService, node *interfaces.LogicDefinitionNode,
	allNodes []*interfaces.LogicDefinitionNode, nodeMap map[string]struct{}) error {
	// Only two view joins are supported
	if len(node.Inputs) != 2 {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
			WithErrorDetails("The logic definition join config is invalid, only support two views join")
	}

	// Check whether the input nodes are duplicated
	inputNodesMap := make(map[string]struct{})
	for _, inputNode := range node.Inputs {
		if _, ok := inputNodesMap[inputNode]; ok {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
				WithErrorDetails("The logic definition join config is invalid, inputs must be unique")
		}
		inputNodesMap[inputNode] = struct{}{}
	}

	// Verify whether the input node exists
	for _, inputNode := range node.Inputs {
		if _, ok := nodeMap[inputNode]; !ok {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
				WithErrorDetails(fmt.Sprintf("The logic definition join config is invalid, input '%s' is not exist", inputNode))
		}
	}

	// mapstructure parses join_on
	var cfg interfaces.JoinNodeCfg
	err := mapstructure.Decode(node.Config, &cfg)
	if err != nil {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
			WithErrorDetails("The logic definition join config is invalid")
	}

	// The join_type can only be inner, left or right
	if _, ok := interfaces.JoinTypeMap[cfg.JoinType]; !ok {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_JoinType).
			WithErrorDetails("The logic definition join config is invalid, join_type must be inner, left, right")
	}

	// join_on verification
	if len(cfg.JoinOn) == 0 {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
			WithErrorDetails("The logic definition join config is invalid, join_on must be set")
	}

	// join_on verification
	for _, joinOn := range cfg.JoinOn {
		if joinOn.LeftField == "" || joinOn.RightField == "" {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
				WithErrorDetails("The logic definition join config is invalid, join_on left_field and right_field must be set")
		}

		// The operator must only be =
		if joinOn.Operator != "=" {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
				WithErrorDetails("The logic definition join config is invalid, join_on operator must be =")
		}
	}

	// The verification output field cannot be empty
	if len(node.OutputFields) == 0 {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
			WithErrorDetails("Join node must have output fields")
	}

	// Verify the output field format: The join node only supports mapping mode
	// First, obtain the output fields of all input nodes
	nodeCache := make(map[string]map[string]*interfaces.Property)
	inputFieldsMap := make(map[string]map[string]*interfaces.Property)
	for _, inputID := range node.Inputs {
		fieldsMap, err := getNodeOutputFieldsMap(ctx, rs, inputID, allNodes, nodeCache)
		if err != nil {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
				WithErrorDetails(fmt.Sprintf("Failed to get output fields from input node '%s': %v", inputID, err))
		}
		inputFieldsMap[inputID] = fieldsMap
	}

	// Verify each output field
	for _, field := range node.OutputFields {
		// The Join node does not support wildcard mode
		if field.Name == "*" {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
				WithErrorDetails("Join node does not support wildcard field '*'")
		}

		// Mapping mode: from and from_node must be specified
		if field.From == "" || field.FromNode == "" {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
				WithErrorDetails(fmt.Sprintf("Join node output field '%s' must have 'from' and 'from_node' configuration", field.Name))
		}

		// Mapping mode: There should be no FromList configuration
		if len(field.FromList) > 0 {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
				WithErrorDetails(fmt.Sprintf("Join node output field '%s' should not have 'from_list' configuration", field.Name))
		}

		// Verify whether from_node is in the input node
		found := false
		for _, inputNode := range node.Inputs {
			if inputNode == field.FromNode {
				found = true
				break
			}
		}
		if !found {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
				WithErrorDetails(fmt.Sprintf("Join node output field '%s' references non-existent input node '%s'", field.Name, field.FromNode))
		}

		// Verify whether the "from" field exists in the source node
		if sourceFields, ok := inputFieldsMap[field.FromNode]; ok {
			if _, exists := sourceFields[field.From]; !exists {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
					WithErrorDetails(fmt.Sprintf("Join node output field '%s' references non-existent field '%s' in node '%s'",
						field.Name, field.From, field.FromNode))
			}
		}
	}

	return nil
}

func validateUnionNode(ctx context.Context, rs *resourceService, category string, node *interfaces.LogicDefinitionNode,
	allNodes []*interfaces.LogicDefinitionNode, nodeMap map[string]struct{}) error {
	// Currently, only two view unions are supported
	if len(node.Inputs) < 2 {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
			WithErrorDetails("The logic definition union config is invalid, need at least two views union")
	}

	// Check whether the input nodes are duplicated
	inputNodesMap := make(map[string]struct{})
	for _, inputNode := range node.Inputs {
		if _, ok := inputNodesMap[inputNode]; ok {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
				WithErrorDetails("The logic definition union config is invalid, inputs must be unique")
		}
		inputNodesMap[inputNode] = struct{}{}
	}

	// Verify whether the input node exists
	for _, inputNode := range node.Inputs {
		if _, ok := nodeMap[inputNode]; !ok {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
				WithErrorDetails(fmt.Sprintf("The logic definition union config is invalid, input '%s' is not exist", inputNode))
		}
	}

	// mapstructure parses union config
	var cfg interfaces.UnionNodeCfg
	err := mapstructure.Decode(node.Config, &cfg)
	if err != nil {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
			WithErrorDetails("The logic definition union config is invalid")
	}

	if _, ok := interfaces.UnionTypeMap[cfg.UnionType]; !ok {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
			WithErrorDetails("The logic definition union config is invalid, union_type must be all, distinct")
	}

	// If it is an index resource, only union all is allowed
	if category == interfaces.ResourceCategoryIndex {
		if cfg.UnionType != interfaces.UnionType_All {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
				WithErrorDetails("The logic definition union config is invalid, DSL or IndexBase view only support union all")
		}
	}

	// The verification output field cannot be empty
	if len(node.OutputFields) == 0 {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
			WithErrorDetails("Union node must have output fields")
	}

	// Verify the output field format: union nodes only support alignment mode
	// First, obtain the output fields of all input nodes
	nodeCache := make(map[string]map[string]*interfaces.Property)
	inputFieldsMap := make(map[string]map[string]*interfaces.Property)
	for _, inputID := range node.Inputs {
		fieldsMap, err := getNodeOutputFieldsMap(ctx, rs, inputID, allNodes, nodeCache)
		if err != nil {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
				WithErrorDetails(fmt.Sprintf("Failed to get output fields from input node '%s': %v", inputID, err))
		}
		inputFieldsMap[inputID] = fieldsMap
	}

	for _, field := range node.OutputFields {
		// Union nodes do not support wildcard mode
		if field.Name == "*" {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
				WithErrorDetails("Union node does not support wildcard field '*'")
		}

		// Alignment mode: FromList configuration is required
		if len(field.FromList) == 0 {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
				WithErrorDetails(fmt.Sprintf("Union node output field '%s' must have 'from_list' configuration", field.Name))
		}

		// Alignment mode: There should not be separate from and from_node configurations (unless used in FromList)
		if field.From != "" || field.FromNode != "" {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
				WithErrorDetails(fmt.Sprintf("Union node output field '%s' should not have 'from' or 'from_node' at field level, use 'from_list' instead", field.Name))
		}

		// Verify whether the length of FromList is consistent with that of inputs
		if len(field.FromList) != len(node.Inputs) {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
				WithErrorDetails(fmt.Sprintf("The union output field '%s' from list count (%d) not equal inputs count (%d)",
					field.Name, len(field.FromList), len(node.Inputs)))
		}

		// Verify whether each reference in FromList points to a valid input node and field
		for _, ref := range field.FromList {
			found := false
			for _, inputNode := range node.Inputs {
				if inputNode == ref.FromNode {
					found = true
					break
				}
			}
			if !found {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
					WithErrorDetails(fmt.Sprintf("Union node output field '%s' references non-existent input node '%s' in from_list", field.Name, ref.FromNode))
			}

			// Verify whether the "from" field exists in the source node
			if ref.From != "" {
				if sourceFields, ok := inputFieldsMap[ref.FromNode]; ok {
					if _, exists := sourceFields[ref.From]; !exists {
						return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
							WithErrorDetails(fmt.Sprintf("Union node output field '%s' references non-existent field '%s' in node '%s'",
								field.Name, ref.From, ref.FromNode))
					}
				}
			}
		}
	}

	return nil
}

func validateSqlNode(ctx context.Context, rs *resourceService, node *interfaces.LogicDefinitionNode,
	allNodes []*interfaces.LogicDefinitionNode, nodeMap map[string]struct{}) error {
	// The input node cannot be empty
	if len(node.Inputs) == 0 {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
			WithErrorDetails("The logic definition sql config is invalid, inputs must be set")
	}

	// Check whether the input nodes are duplicated
	inputNodesMap := make(map[string]struct{})
	for _, inputNode := range node.Inputs {
		if _, ok := inputNodesMap[inputNode]; ok {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
				WithErrorDetails("The logic definition sql config is invalid, inputs must be unique")
		}
		inputNodesMap[inputNode] = struct{}{}
	}

	// Verify whether the input node exists
	for _, inputNode := range node.Inputs {
		if _, ok := nodeMap[inputNode]; !ok {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
				WithErrorDetails(fmt.Sprintf("The logic definition sql config is invalid, input '%s' is not exist", inputNode))
		}
	}

	// mapstructure parses sql config
	var cfg interfaces.SQLNodeCfg
	err := mapstructure.Decode(node.Config, &cfg)
	if err != nil {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
			WithErrorDetails("The logic definition sql config is invalid")
	}

	// Verify whether the sql is empty
	if cfg.SQL == "" {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
			WithErrorDetails("The logic definition sql config is invalid, sql must be set")
	}

	// Verify whether the SQL syntax is correct
	if err := validateSQLSyntax(ctx, cfg.SQL); err != nil {
		return err
	}

	// The verification output field cannot be empty
	if len(node.OutputFields) == 0 {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
			WithErrorDetails("SQL node must have output fields")
	}

	// Verify the output field format: sql nodes support definition mode and wildcard mode
	for _, field := range node.OutputFields {
		// Wildcard mode: Only "*" is allowed
		if field.Name == "*" {
			// In wildcard mode, no other field configurations should be allowed (but type is permitted for type inference).
			if field.From != "" || field.FromNode != "" || len(field.FromList) > 0 {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
					WithErrorDetails("Wildcard field '*' in SQL node should not have from, from_node or from_list configuration")
			}
			continue
		}

		// Definition mode: There should be no mapping or alignment configuration (SQL nodes define fields by themselves)
		if field.From != "" || field.FromNode != "" || len(field.FromList) > 0 {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
				WithErrorDetails(fmt.Sprintf("SQL node output field '%s' should not have from, from_node or from_list configuration", field.Name))
		}
	}

	return nil
}

func validateOutputNode(ctx context.Context, rs *resourceService, node *interfaces.LogicDefinitionNode,
	allNodes []*interfaces.LogicDefinitionNode, nodeMap map[string]struct{}) error {
	// There can only be one input node
	if len(node.Inputs) != 1 {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
			WithErrorDetails("The output node must have one input node")
	}

	// Verify whether the input node exists
	inputNode := node.Inputs[0]
	if _, ok := nodeMap[inputNode]; !ok {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
			WithErrorDetails(fmt.Sprintf("The output node input '%s' is not exist", inputNode))
	}

	if len(node.OutputFields) == 0 {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
			WithErrorDetails("The output node must have output fields")
	}

	// Verify the output field format: The output node supports wildcard mode and projection mode
	// Obtain the output field of the input node
	nodeCache := make(map[string]map[string]*interfaces.Property)
	inputNodeID := node.Inputs[0]
	inputFieldsMap, err := getNodeOutputFieldsMap(ctx, rs, inputNodeID, allNodes, nodeCache)
	if err != nil {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
			WithErrorDetails(fmt.Sprintf("Failed to get output fields from input node '%s': %v", inputNodeID, err))
	}

	for _, field := range node.OutputFields {
		// Wildcard mode: Only "*" is allowed
		if field.Name == "*" {
			// In wildcard mode, no other field configurations should be provided
			if field.Type != "" || field.From != "" || field.FromNode != "" || len(field.FromList) > 0 {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
					WithErrorDetails("Wildcard field '*' should not have additional configuration")
			}
			continue
		}

		// Projection mode: Only field names are allowed; there should be no mapping or alignment configurations
		if field.From != "" || field.FromNode != "" || len(field.FromList) > 0 {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
				WithErrorDetails(fmt.Sprintf("Output node field '%s' should not have from, from_node or from_list configuration", field.Name))
		}

		// Check whether the verification field exists in the input node
		if _, ok := inputFieldsMap[field.Name]; !ok {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
				WithErrorDetails(fmt.Sprintf("Output node field '%s' is not in the input node '%s' output fields", field.Name, inputNodeID))
		}
	}

	// Verify that the name cannot be repeated and the display_name cannot be repeated
	nameMap := make(map[string]struct{})
	displayNameMap := make(map[string]struct{})
	for _, field := range node.OutputFields {
		if _, ok := nameMap[field.Name]; ok {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
				WithErrorDetails("The output node field name is repeated")
		}
		nameMap[field.Name] = struct{}{}

		if _, ok := displayNameMap[field.DisplayName]; ok {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
				WithErrorDetails("The output node field display_name is repeated")
		}
		displayNameMap[field.DisplayName] = struct{}{}
	}

	return nil
}

// Compared with the validation at the handler layer, supplement the validation of the filter condition field types
func validateCond(ctx context.Context, cfg *interfaces.FilterCondCfg, fieldsMap map[string]*interfaces.Property) error {
	if cfg == nil {
		return nil
	}

	// Determine whether the filter is an empty object {}
	if cfg.Name == "" && cfg.Operation == "" && len(cfg.SubConds) == 0 && cfg.ValueFrom == "" && cfg.Value == nil {
		return nil
	}

	// The filter condition field does not allow __id and __routing
	if cfg.Name == "__id" || cfg.Name == "__routing" {
		return rest.NewHTTPError(ctx, http.StatusForbidden, verrors.VegaBackend_InvalidParameter_FilterCondition).
			WithErrorDetails("The filter field '__id' and '__routing' is not allowed")
	}

	// Filtering operator
	if cfg.Operation == "" {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_NullParameter_FilterConditionOperation)
	}

	_, exists := fcond.OperationMap[cfg.Operation]
	if !exists {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_UnsupportFilterConditionOperation).
			WithErrorDetails(fmt.Sprintf("unsupport condition operation %s", cfg.Operation))
	}

	switch cfg.Operation {
	case fcond.OperationAnd, fcond.OperationOr:
		// The number of sub-filtering conditions cannot exceed 10
		if len(cfg.SubConds) > interfaces.MaxSubCondition {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_CountExceeded_FilterConditionSubConds).
				WithErrorDetails(fmt.Sprintf("The number of subConditions exceeds %d", interfaces.MaxSubCondition))
		}

		for _, subCond := range cfg.SubConds {
			err := validateCond(ctx, subCond, fieldsMap)
			if err != nil {
				return err
			}
		}
	default:
		// The name of the filter field cannot be empty
		if cfg.Name == "" {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_NullParameter_FilterConditionName)
		}
	}

	switch cfg.Operation {
	case fcond.OperationEqual, fcond.OperationNotEqual, fcond.OperationGt, fcond.OperationGte,
		fcond.OperationLt, fcond.OperationLte, fcond.OperationLike, fcond.OperationNotLike,
		fcond.OperationRegex, fcond.OperationMatch, fcond.OperationMatchPhrase, fcond.OperationCurrent:
		// The value on the right is a single value
		_, ok := cfg.Value.([]interface{})
		if ok {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_FilterConditionValue).
				WithErrorDetails(fmt.Sprintf("[%s] operation's value should be a single value", cfg.Operation))
		}

		if cfg.Operation == fcond.OperationLike || cfg.Operation == fcond.OperationNotLike ||
			cfg.Operation == fcond.OperationPrefix || cfg.Operation == fcond.OperationNotPrefix {
			_, ok := cfg.Value.(string)
			if !ok {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_FilterConditionValue).
					WithErrorDetails("[like not_like prefix not_prefix] operation's value should be a string")
			}
		}

		if cfg.Operation == fcond.OperationRegex {
			val, ok := cfg.Value.(string)
			if !ok {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_FilterConditionValue).
					WithErrorDetails("[regex] operation's value should be a string")
			}

			_, err := regexp2.Compile(val, regexp2.RE2)
			if err != nil {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_FilterConditionValue).
					WithErrorDetails(fmt.Sprintf("[regex] operation regular expression error: %s", err.Error()))
			}

		}

	case fcond.OperationIn, fcond.OperationNotIn:
		// When operation is in and not_in, the value is an array of any basic type and its length is greater than or equal to 1.
		_, ok := cfg.Value.([]interface{})
		if !ok {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_FilterConditionValue).
				WithErrorDetails("[in not_in] operation's value must be an array")
		}

		if len(cfg.Value.([]interface{})) <= 0 {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_FilterConditionValue).
				WithErrorDetails("[in not_in] operation's value should contains at least 1 value")
		}
	case fcond.OperationRange, fcond.OperationOutRange, fcond.OperationBetween:
		// When operation is range, value is a numeric array of length 2 composed of the lower and upper boundaries of the range
		// When the operation is out_range, the value is an array of numeric types with a length of 2, and the range of the queried data is (-inf, value[0]) / [value[1], +inf).
		v, ok := cfg.Value.([]interface{})
		if !ok {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_FilterConditionValue).
				WithErrorDetails("[range, out_range, between] operation's value must be an array")
		}

		if len(v) != 2 {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_FilterConditionValue).
				WithErrorDetails("[range, out_range, between] operation's value must contain 2 values")
		}
	case fcond.OperationBefore:
		// before, an array of length 2, with the first value being the time length, is of numeric type; The second value is a time unit, a string
		_, ok := cfg.Value.(float64)
		if !ok {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_FilterConditionValue).
				WithErrorDetails("[before] operation's value must be an array")
		}

		_, ok = cfg.RemainCfg["unit"]
		if !ok {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_FilterConditionValue).
				WithErrorDetails("[before] operation's remain cfg must contain unit")
		}
	}

	switch cfg.Operation {
	case fcond.OperationAnd, fcond.OperationOr:
		for _, subCond := range cfg.SubConds {
			err := validateCond(ctx, subCond, fieldsMap)
			if err != nil {
				return err
			}
		}
	default:
		// The filter fields other than * are in the view field list
		if cfg.Name != interfaces.AllField {
			cField, ok := fieldsMap[cfg.Name]
			if !ok {
				return rest.NewHTTPError(ctx, http.StatusForbidden, verrors.VegaBackend_InvalidParameter_FilterCondition).
					WithErrorDetails(fmt.Sprintf("Filter field '%s' is not in view fields list", cfg.Name))
			}

			fieldType := cField.Type
			// binary type fields do not support filtering
			if fieldType == interfaces.DataType_Binary {
				return rest.NewHTTPError(ctx, http.StatusForbidden, verrors.VegaBackend_InvalidParameter_FilterCondition).
					WithErrorDetails("Binary fields do not support filtering")
			}

			// The field type of empty, not_empty must be string
			if cfg.Operation == fcond.OperationEmpty || cfg.Operation == fcond.OperationNotEmpty {
				if !interfaces.DataType_IsString(fieldType) {
					return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_FilterCondition).
						WithErrorDetails("Filter field must be of string type when using 'empty' or 'not_empty' operation")
				}
			}
		} else {
			// If the field is *, only the match and match_phrase operators are allowed
			if cfg.Operation != fcond.OperationMatch && cfg.Operation != fcond.OperationMatchPhrase &&
				cfg.Operation != fcond.OperationMultiMatch {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_FilterCondition).
					WithErrorDetails("Filter field '*' only supports 'match', 'match_phrase' and 'multi_match' operations")
			}
		}
	}

	return nil
}

// Parse the logicDefinition and generate the schemaDefinition
func (rs *resourceService) parseLogicDefinition(ctx context.Context,
	logicDefinition []*interfaces.LogicDefinitionNode) ([]*interfaces.Property, error) {

	// 1. Build a node mapping table
	nodes := make(map[string]*interfaces.LogicDefinitionNode)
	for _, node := range logicDefinition {
		nodes[node.ID] = node
	}

	// 2. Locate the terminal output node (output node
	var outputNode *interfaces.LogicDefinitionNode
	for _, node := range logicDefinition {
		if node.Type == interfaces.LogicDefinitionNodeType_Output {
			outputNode = node
			break
		}
	}

	if outputNode == nil {
		// If the output node is not explicitly defined, take the last node as the fallback
		if len(logicDefinition) > 0 {
			outputNode = logicDefinition[len(logicDefinition)-1]
		} else {
			return nil, fmt.Errorf("logic definition is empty")
		}
	}

	// 3. Recursively parse field metadata (with cache to avoid double counting)
	memo := make(map[string][]*interfaces.Property)
	var resolve func(nodeID string) ([]*interfaces.Property, error)
	resolve = func(nodeID string) ([]*interfaces.Property, error) {
		if cached, ok := memo[nodeID]; ok {
			return cached, nil
		}

		node, ok := nodes[nodeID]
		if !ok {
			return nil, fmt.Errorf("node %s not found in logic definition", nodeID)
		}

		var result []*interfaces.Property
		var inputFieldsMap = make(map[string][]*interfaces.Property)
		var sourceResourceFields []*interfaces.Property

		// Handle leaf nodes: Resource nodes
		if node.Type == interfaces.LogicDefinitionNodeType_Resource {
			var cfg interfaces.ResourceNodeCfg
			if err := mapstructure.Decode(node.Config, &cfg); err != nil {
				return nil, fmt.Errorf("decode resource node config failed: %w", err)
			}
			res, err := rs.GetByID(ctx, cfg.ResourceID)
			if err != nil {
				return nil, fmt.Errorf("get resource %s failed: %w", cfg.ResourceID, err)
			}
			sourceResourceFields = res.SchemaDefinition
		} else {
			// Parse the output fields of all input nodes
			for _, inputID := range node.Inputs {
				fields, err := resolve(inputID)
				if err != nil {
					return nil, err
				}
				inputFieldsMap[inputID] = fields
			}
		}

		// Handle the output_fields of the current node
		for _, vProp := range node.OutputFields {
			if vProp.Name == "*" {
				// Wildcard mode: Fully transparent upstream fields
				if node.Type == interfaces.LogicDefinitionNodeType_Resource {
					for _, f := range sourceResourceFields {
						result = append(result, copyProperty(f))
					}
				} else {
					for _, inputID := range node.Inputs {
						for _, f := range inputFieldsMap[inputID] {
							result = append(result, copyProperty(f))
						}
					}
				}
				continue
			}

			// Projection/Mapping/Alignment/Definition pattern: Construct Property
			prop := &interfaces.Property{
				Name:         vProp.Name,
				Type:         vProp.Type,
				DisplayName:  vProp.DisplayName,
				OriginalName: vProp.OriginalName,
				Description:  vProp.Description,
				Features:     vProp.Features,
			}

			// Recursive traceability completion metadata (Type, DisplayName, Description, OriginalName, Features)
			var sourceProp *interfaces.Property
			if node.Type == interfaces.LogicDefinitionNodeType_Resource {
				// The Resource node is found from the physical Schema
				for _, f := range sourceResourceFields {
					if f.Name == vProp.Name {
						sourceProp = f
						break
					}
				}
			} else if vProp.From != "" && vProp.FromNode != "" {
				// Mapping mode (Join) : Clearly specifies the source node and field
				if sFields, ok := inputFieldsMap[vProp.FromNode]; ok {
					for _, f := range sFields {
						if f.Name == vProp.From {
							sourceProp = f
							break
						}
					}
				}
			} else if len(vProp.FromList) > 0 {
				// Alignment mode (Union) : Retrieve metadata from the first matching source node
				for _, ref := range vProp.FromList {
					if sFields, ok := inputFieldsMap[ref.FromNode]; ok {
						for _, f := range sFields {
							if f.Name == ref.From {
								sourceProp = f
								break
							}
						}
					}
					if sourceProp != nil {
						break
					}
				}
			} else {
				// Projection mode /SQL definition: Search in the upstream input by name
				for _, inputID := range node.Inputs {
					if sFields, ok := inputFieldsMap[inputID]; ok {
						for _, f := range sFields {
							if f.Name == vProp.Name {
								sourceProp = f
								break
							}
						}
					}
					if sourceProp != nil {
						break
					}
				}
			}

			// If the source field is found, complete the missing information
			if sourceProp != nil {
				fillMissingMetadata(prop, sourceProp)
			}
			result = append(result, prop)
		}

		memo[nodeID] = result
		return result, nil
	}

	return resolve(outputNode.ID)
}

func copyProperty(p *interfaces.Property) *interfaces.Property {
	if p == nil {
		return nil
	}
	cp := *p
	if len(p.Features) > 0 {
		cp.Features = make([]interfaces.PropertyFeature, len(p.Features))
		copy(cp.Features, p.Features)
	}
	return &cp
}

func fillMissingMetadata(target, source *interfaces.Property) {
	if target.Type == "" {
		target.Type = source.Type
	}
	if target.DisplayName == "" {
		target.DisplayName = source.DisplayName
	}
	if target.Description == "" {
		target.Description = source.Description
	}
	if target.OriginalName == "" {
		target.OriginalName = source.OriginalName
	}
	if len(target.Features) == 0 {
		target.Features = source.Features
	}
}

// validateSQLSyntax verifies whether the SQL syntax is correct
// First, replace the variables in SQL (such as.node1) with placeholders
// 2. Then verify using standard SQL syntax rules
func validateSQLSyntax(ctx context.Context, sql string) error {
	if sql == "" {
		return nil // The empty SQL has been processed in the previous validation
	}

	// Step 1: Replace the variables in SQL (such as.node1,.node2, etc.) with placeholders
	// Matching pattern: dots followed by identifiers, such as.node1,.my_table
	nodeVarRegex := regexp.MustCompile(`\.[a-zA-Z_][a-zA-Z0-9_]*`)
	cleanedSQL := nodeVarRegex.ReplaceAllString(sql, " placeholder_table ")

	// Step 2: Standard SQL syntax validation
	// 2.1 Check if it starts WITH "SELECT" or "with"
	trimmedSQL := strings.TrimSpace(strings.ToUpper(cleanedSQL))
	if !strings.HasPrefix(trimmedSQL, "SELECT") && !strings.HasPrefix(trimmedSQL, "WITH") {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
			WithErrorDetails("SQL must start with SELECT or WITH clause")
	}

	// 2.2 Check if the parentheses match
	openParen := strings.Count(cleanedSQL, "(")
	closeParen := strings.Count(cleanedSQL, ")")
	if openParen != closeParen {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
			WithErrorDetails(fmt.Sprintf("Unbalanced parentheses: %d opening vs %d closing", openParen, closeParen))
	}

	// 2.3 Check for common grammar errors
	if err := checkCommonSQLErrors(ctx, cleanedSQL); err != nil {
		return err
	}

	return nil
}

// checkCommonSQLErrors checks for common SQL syntax errors
func checkCommonSQLErrors(ctx context.Context, sql string) error {
	upperSQL := strings.ToUpper(sql)
	trimmedSQL := strings.TrimSpace(sql)

	// Check for duplicate keywords
	duplicatePatterns := []struct {
		pattern *regexp.Regexp
		message string
	}{
		{regexp.MustCompile(`\bFROM\s+FROM\b`), "Duplicate FROM keyword"},
		{regexp.MustCompile(`\bSELECT\s+SELECT\b`), "Duplicate SELECT keyword"},
		{regexp.MustCompile(`\bWHERE\s+WHERE\b`), "Duplicate WHERE keyword"},
		{regexp.MustCompile(`\bJOIN\s+JOIN\b`), "Duplicate JOIN keyword"},
		{regexp.MustCompile(`\bGROUP\s+BY\s+BY\b`), "Duplicate BY in GROUP BY"},
		{regexp.MustCompile(`\bORDER\s+BY\s+BY\b`), "Duplicate BY in ORDER BY"},
	}

	for _, dp := range duplicatePatterns {
		if dp.pattern.MatchString(upperSQL) {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
				WithErrorDetails(fmt.Sprintf("SQL syntax error: %s", dp.message))
		}
	}

	// Check if there is a table name after "FROM" (priority check)
	fromWithoutTable := regexp.MustCompile(`(?i)\bFROM\s*$`)
	if fromWithoutTable.MatchString(trimmedSQL) {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
			WithErrorDetails("SQL syntax error: FROM clause must specify a table")
	}

	// Check if there is a FROM after SELECT (simple check)
	if strings.HasPrefix(upperSQL, "SELECT") {
		// Check if the "FROM" keyword is included
		if !strings.Contains(upperSQL, " FROM ") && !strings.HasSuffix(upperSQL, " FROM") {
			// Check if it is in the simple form of SELECT * or SELECT 1 (without FROM)
			simpleSelectRegex := regexp.MustCompile(`(?i)^SELECT\s+[*\d]+\s*$`)
			if !simpleSelectRegex.MatchString(trimmedSQL) {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
					WithErrorDetails("SQL syntax error: SELECT statement must contain a FROM clause")
			}
		}
	}

	// Check if there are any conditions after "WHERE"
	whereWithoutCondition := regexp.MustCompile(`(?i)\bWHERE\s*$`)
	if whereWithoutCondition.MatchString(trimmedSQL) {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
			WithErrorDetails("SQL syntax error: WHERE clause must have a condition")
	}

	// Check if there is a column after GROUP BY
	groupByWithoutColumn := regexp.MustCompile(`(?i)\bGROUP\s+BY\s*$`)
	if groupByWithoutColumn.MatchString(trimmedSQL) {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
			WithErrorDetails("SQL syntax error: GROUP BY must have at least one column")
	}

	// Check if there is a column name after ORDER BY
	orderByWithoutColumn := regexp.MustCompile(`(?i)\bORDER\s+BY\s*$`)
	if orderByWithoutColumn.MatchString(trimmedSQL) {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
			WithErrorDetails("SQL syntax error: ORDER BY must have at least one column")
	}

	return nil
}
