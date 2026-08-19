// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knlogicpropertyresolver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

const (
	// defaultMaxConcurrency default maximum number of concurrencies.
	defaultMaxConcurrency = 4
)

// KnLogicPropertyResolverService logical propertyparseserviceimplements.
type knLogicPropertyResolverService struct {
	logger              interfaces.Logger
	bknBackendAccess    interfaces.BknBackendAccess
	ontologyQueryClient interfaces.DrivenOntologyQuery
	dynamicLLM          *dynamicParamsLLM // Metric and ToolBox-tool dynamic parameter generator.
}

var (
	serviceOnce sync.Once
	service     interfaces.IKnLogicPropertyResolverService
)

// NewKnLogicPropertyResolverService createlogical propertyparseservice.
func NewKnLogicPropertyResolverService() interfaces.IKnLogicPropertyResolverService {
	serviceOnce.Do(func() {
		conf := config.NewConfigLoader()
		service = &knLogicPropertyResolverService{
			logger:              conf.GetLogger(),
			bknBackendAccess:    drivenadapters.NewBknBackendAccess(),
			ontologyQueryClient: drivenadapters.NewOntologyQueryAccess(),
			dynamicLLM:          newDynamicParamsLLM(conf.GetLogger(), drivenadapters.NewMFModelAPIClient(), drivenadapters.NewOperatorIntegrationClient()),
		}
	})
	return service
}

// ResolveLogicProperties parselogical property.
func (s *knLogicPropertyResolverService) ResolveLogicProperties(
	ctx context.Context,
	req *interfaces.ResolveLogicPropertiesRequest,
) (*interfaces.ResolveLogicPropertiesResponse, error) {
	// Simplified logging: Handler layer has recorded detailed request parameters.
	s.logger.WithContext(ctx).Debugf("[Service] 开始处理 %d 个逻辑属性", len(req.Properties))

	// Set default Options.
	if req.Options == nil {
		req.Options = &interfaces.ResolveOptions{
			ReturnDebug:     false,
			MaxRepairRounds: 1,
			MaxConcurrency:  defaultMaxConcurrency,
		}
	}

	// Step 1: parametervalidate.
	if err := s.validateRequest(req); err != nil {
		return nil, err
	}

	// Step 2: Get the object type definition.
	s.logger.WithContext(ctx).Debugf("[Step 1] 获取对象类定义: kn_id=%s, ot_id=%s", req.KnID, req.OtID)
	objectType, err := s.getObjectTypeDefinition(ctx, req.KnID, req.OtID)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("[Step 1] ❌ 失败: %v", err)
		return nil, err
	}
	s.logger.WithContext(ctx).Debugf("[Step 1] ✅ 成功")

	// Step 3: Extract logical attribute definitions.
	logicPropertiesDef, err := s.extractLogicProperties(ctx, objectType, req.Properties)
	if err != nil {
		return nil, err
	}

	// Initialize debug information collector.
	var debugCollector *DebugCollector
	if req.Options.ReturnDebug {
		debugCollector = NewDebugCollector()
		debugCollector.SetTraceID("")
		debugCollector.SetNowMs(time.Now().UnixMilli())
	}

	// Step 4: Generate dynamic_params.
	s.logger.WithContext(ctx).Debugf("[Step 2] 生成 dynamic_params（Agent 并发调用）")
	startTime := time.Now()
	dynamicParams, missingParams, genFailures, err := s.generateDynamicParams(ctx, req, logicPropertiesDef, debugCollector)
	generateParamsDuration := time.Since(startTime)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("[Step 2] ❌ 失败: %v", err)
		return nil, err
	}

	// Parameter generation failure (LLM / dependent service exception) is reported prior to missing parameters: this is not because the caller passes fewer parameters.
	// It is a server link failure. The upstream code/detail must be exposed as it is and cannot be disguised as MISSING_INPUT_PARAMS.
	if len(genFailures) > 0 {
		s.logger.WithContext(ctx).Errorf("[Step 2] ❌ 参数生成失败: %d 个属性", len(genFailures))
		if req.Options.ReturnDebug {
			return &interfaces.ResolveLogicPropertiesResponse{
				Datas: []map[string]any{},
				Debug: debugCollector.BuildDebugInfo(),
			}, nil
		}
		return nil, s.buildGenerationFailedError(ctx, genFailures)
	}

	// If there are missing parameters, the processing method depends on whether debug is turned on or not.
	if len(missingParams) > 0 {
		s.logger.WithContext(ctx).Warnf("[Step 2] ⚠️ 存在缺参: %d 个属性", len(missingParams))

		// Special processing: If debug is turned on, normal response will be returned and error information will be placed in debug.
		if req.Options.ReturnDebug {
			s.logger.WithContext(ctx).Infof("[Step 2] 🔍 Debug模式：缺参场景返回正常响应，错误信息放在 debug 中")

			// Construct a normal response, datas is an empty array.
			debugInfo := debugCollector.BuildDebugInfo()
			return &interfaces.ResolveLogicPropertiesResponse{
				Datas: []map[string]any{}, // Empty array because there is no success data.
				Debug: debugInfo,
			}, nil
		}

		// Debug is not turned on: keep the existing behavior and throw an error.
		missingError := s.buildMissingParamsError(ctx, missingParams, nil)
		return nil, missingError
	}
	s.logger.WithContext(ctx).Infof("⏱️ [耗时] 生成动态参数: %dms", generateParamsDuration.Milliseconds())

	// Step 5: Call ontology-query to query logical attribute values.
	s.logger.WithContext(ctx).Debugf("[Step 3] 调用 ontology-query 查询属性值")
	startTime = time.Now()
	result, err := s.queryLogicProperties(ctx, req, dynamicParams)
	queryDuration := time.Since(startTime)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("[Step 3] ❌ 失败: %v", err)
		return nil, err
	}
	s.logger.WithContext(ctx).Infof("⏱️ [耗时] 查询属性值: %dms", queryDuration.Milliseconds())

	// Step 6: buildresponse.
	resp := &interfaces.ResolveLogicPropertiesResponse{
		Datas: result,
	}

	// Return debug information if necessary.
	if req.Options.ReturnDebug {
		debugCollector.SetNowMs(time.Now().UnixMilli())
		resp.Debug = debugCollector.BuildDebugInfo()
	}

	s.logger.WithContext(ctx).Debugf("[KnLogicPropertyResolver] Resolve logic properties successfully")
	return resp, nil
}

// ValidateRequest validaterequestparameter.
func (s *knLogicPropertyResolverService) validateRequest(req *interfaces.ResolveLogicPropertiesRequest) error {
	if req.KnID == "" {
		return fmt.Errorf("kn_id is required")
	}
	if req.OtID == "" {
		return fmt.Errorf("ot_id is required")
	}
	if req.Query == "" {
		return fmt.Errorf("query is required")
	}
	if len(req.InstanceIdentities) == 0 {
		return fmt.Errorf("_instance_identities is required and cannot be empty")
	}
	if len(req.Properties) == 0 {
		return fmt.Errorf("properties is required and cannot be empty")
	}
	return nil
}

// getObjectTypeDefinition Gets the object type definition.
func (s *knLogicPropertyResolverService) getObjectTypeDefinition(
	ctx context.Context,
	knID string,
	otID string,
) (*interfaces.ObjectType, error) {
	s.logger.WithContext(ctx).Debugf("[KnLogicPropertyResolver] Getting object type definition: kn_id=%s, ot_id=%s", knID, otID)

	// Call BKN backend to get object type definition (include_detail=true to get logic_properties)
	objectTypes, err := s.bknBackendAccess.GetObjectTypeDetail(ctx, knID, []string{otID}, true)
	if err != nil {
		return nil, err
	}

	// Check the returned results.
	if len(objectTypes) == 0 {
		return nil, errors.DefaultHTTPError(ctx, http.StatusNotFound,
			fmt.Sprintf("object type %s not found in knowledge network %s", otID, knID))
	}

	// Returns the first object type definition (we only requested one otID)
	return objectTypes[0], nil
}

// extractLogicProperties Extracts logical property definitions from object type definitions.
func (s *knLogicPropertyResolverService) extractLogicProperties(
	ctx context.Context,
	objectType *interfaces.ObjectType,
	properties []string,
) (map[string]*interfaces.LogicPropertyDef, error) {
	s.logger.WithContext(ctx).Debugf("[KnLogicPropertyResolver] Extracting logic properties: %v", properties)

	// Check if objectType.LogicProperties is empty.
	if len(objectType.LogicProperties) == 0 {
		s.logger.WithContext(ctx).Warnf("[KnLogicPropertyResolver] Object type %s has no logic properties", objectType.ID)
		return nil, errors.DefaultHTTPError(ctx, http.StatusBadRequest,
			fmt.Sprintf("object type %s has no logic properties defined", objectType.ID))
	}

	// 1. Construct a set of request attributes to facilitate search and verification.
	requestedProps := make(map[string]bool, len(properties))
	for _, prop := range properties {
		requestedProps[prop] = true
	}

	// 2. Traverse objectType.LogicProperties and filter out the requested properties.
	logicPropertiesDef := make(map[string]*interfaces.LogicPropertyDef, len(properties))
	for _, logicProp := range objectType.LogicProperties {
		if requestedProps[logicProp.Name] {
			logicPropertiesDef[logicProp.Name] = logicProp
			s.logger.WithContext(ctx).Debugf("[KnLogicPropertyResolver] Found logic property: %s (type: %s)",
				logicProp.Name, logicProp.Type)
		}
	}

	// 3. Check if all requested attributes are found.
	notFoundProps := []string{}
	for _, prop := range properties {
		if _, found := logicPropertiesDef[prop]; !found {
			notFoundProps = append(notFoundProps, prop)
		}
	}

	// 4. If any property does not exist, return INVALID_PROPERTY error.
	if len(notFoundProps) > 0 {
		s.logger.WithContext(ctx).Errorf("[KnLogicPropertyResolver] Properties not found: %v", notFoundProps)

		// Build a list of available logical properties (for error prompts)
		availableProps := make([]string, 0, len(objectType.LogicProperties))
		for _, logicProp := range objectType.LogicProperties {
			availableProps = append(availableProps, logicProp.Name)
		}

		return nil, errors.DefaultHTTPError(ctx, http.StatusBadRequest,
			fmt.Sprintf("properties not found or not logic properties: %v (available logic properties: %v)",
				notFoundProps, availableProps))
	}
	return logicPropertiesDef, nil
}

// generateDynamicParams generates dynamic_params (concurrency by property)
//
//nolint:unparam // Keep the interface consistent; the error return is for future extension.
func (s *knLogicPropertyResolverService) generateDynamicParams(
	ctx context.Context,
	req *interfaces.ResolveLogicPropertiesRequest,
	logicPropertiesDef map[string]*interfaces.LogicPropertyDef,
	debugCollector *DebugCollector,
) (dynamicParams map[string]interface{}, missingParams []interfaces.MissingPropertyParams,
	genFailures []interfaces.MissingPropertyParams, err error) {
	s.logger.WithContext(ctx).Debugf("[KnLogicPropertyResolver] Generating dynamic params for %d properties", len(logicPropertiesDef))

	// Get concurrent configuration.
	maxConcurrency := req.Options.MaxConcurrency
	if maxConcurrency <= 0 {
		maxConcurrency = 4 // Default number of concurrencies.
	}

	// Step 1: Preparation phase - build property list.
	type PropertyTask struct {
		Name     string
		Property *interfaces.LogicPropertyDef
	}

	tasks := make([]PropertyTask, 0, len(logicPropertiesDef))
	for name, prop := range logicPropertiesDef {
		tasks = append(tasks, PropertyTask{Name: name, Property: prop})
	}

	// Step 2: Call LLM concurrently (unified control of max_concurrency)
	type PropertyResult struct {
		Name          string
		DynamicParams map[string]interface{}
		MissingParams *interfaces.MissingPropertyParams
		Error         error
	}

	// Create a semaphore to control the number of concurrencies.
	semaphore := make(chan struct{}, maxConcurrency)
	results := make(chan PropertyResult, len(tasks))

	// Process each property concurrently.
	for _, task := range tasks {
		go func(t PropertyTask) {
			// Get semaphore.
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			// Collect property type information.
			if debugCollector != nil {
				debugCollector.AddPropertyType(t.Name, string(t.Property.Type))
			}

			// Generate dynamic_params for a single property.
			params, missingParams, err := s.generateSinglePropertyParams(ctx, req, t.Name, t.Property, debugCollector)
			results <- PropertyResult{
				Name:          t.Name,
				DynamicParams: params,
				MissingParams: missingParams,
				Error:         err,
			}
		}(task)
	}

	// Step 3: Collect results.
	dynamicParams = make(map[string]interface{})
	missingParams = []interfaces.MissingPropertyParams{}
	genFailures = []interfaces.MissingPropertyParams{}

	for range len(tasks) {
		result := <-results

		// If there are errors, log but continue processing other properties.
		if result.Error != nil {
			s.logger.WithContext(ctx).Errorf("[KnLogicPropertyResolver] Generate params for property %s failed: %v",
				result.Name, result.Error)
			// Log errors to debug information.
			if debugCollector != nil {
				debugCollector.RecordAgentResponseError(result.Name, result.Error.Error())
			}
			// Generation failure (LLM / dependent service exception) and "model determination missing parameters" are two different things, and are collected separately:
			// Mixing in missingParams will cause 4xx/5xx of dependent services to be wrapped into MISSING_INPUT_PARAMS,
			// Masking the true root cause (issue #450).
			genFailures = append(genFailures, interfaces.MissingPropertyParams{
				Property: result.Name,
				ErrorMsg: fmt.Sprintf("generate params failed: %v", result.Error),
			})
			continue
		}

		// If there are missing parameters, collect missing parameter information.
		if result.MissingParams != nil {
			missingParams = append(missingParams, *result.MissingParams)
			continue
		}

		// Collect successful results.
		// Key fix: need to put the parameter object under the key of property name.
		// ontology-query expected format: {"property_name": {"param1": value1, ...}}
		if result.DynamicParams != nil {
			dynamicParams[result.Name] = result.DynamicParams
			s.logger.WithContext(ctx).Debugf("[KnLogicPropertyResolver] Collected params for %s: %+v",
				result.Name, result.DynamicParams)
		}
	}

	s.logger.WithContext(ctx).Debugf("[KnLogicPropertyResolver] Generated dynamic params for %d properties, %d missing, %d failed",
		len(dynamicParams), len(missingParams), len(genFailures))

	return dynamicParams, missingParams, genFailures, nil
}

// generateSinglePropertyParams generates dynamic_params for a single property.
func (s *knLogicPropertyResolverService) generateSinglePropertyParams(
	ctx context.Context,
	req *interfaces.ResolveLogicPropertiesRequest,
	propertyName string,
	property *interfaces.LogicPropertyDef,
	debugCollector *DebugCollector,
) (dynamicParams map[string]interface{}, missingParams *interfaces.MissingPropertyParams, err error) {
	s.logger.WithContext(ctx).Debugf("[KnLogicPropertyResolver] Generating params for property: %s (type: %s)",
		propertyName, property.Type)

	// According to the attribute type, call the corresponding parameter generation method.
	// Note: Currently implemented using the Agent platform, it can be expanded to support direct calling of LLM in the future.
	switch property.Type {
	case interfaces.LogicPropertyTypeMetric:
		dynamicParams, missingParams, err = s.generateMetricParams(ctx, req, property, propertyName, debugCollector)
	case interfaces.LogicPropertyTypeTool:
		dynamicParams, missingParams, err = s.generateToolParams(ctx, req, property, propertyName, debugCollector)
	default:
		return nil, nil, fmt.Errorf("unknown property type: %s", property.Type)
	}

	if err != nil {
		// Log Agent error response.
		if debugCollector != nil {
			debugCollector.RecordAgentResponseError(propertyName, err.Error())
		}
		return nil, nil, fmt.Errorf("generate params failed: %w", err)
	}

	// Record Agent response information.
	if debugCollector != nil {
		if missingParams != nil {
			debugCollector.RecordAgentResponseMissingParams(propertyName, missingParams)
		} else if dynamicParams != nil {
			debugCollector.RecordAgentResponseSuccess(propertyName, dynamicParams)
		}
	}

	// If there are dynamic_params returned, perform type verification.
	if dynamicParams != nil {
		// Detailed log: check parameter content before verification.
		s.logger.WithContext(ctx).Debugf("[KnLogicPropertyResolver] Validating params for %s (type: %s), params: %+v",
			propertyName, property.Type, dynamicParams)

		switch property.Type {
		case interfaces.LogicPropertyTypeMetric:
			err = s.validateMetricParams(ctx, property, dynamicParams)
		case interfaces.LogicPropertyTypeTool:
			err = s.validateToolParams(ctx, property, dynamicParams)
		}

		if err != nil {
			s.logger.WithContext(ctx).Errorf("[KnLogicPropertyResolver] Validation failed for %s: %v", propertyName, err)
			// When verification fails, a verification error is returned (missingParams is not returned, because this is a verification failure, not a missing parameter)
			return nil, nil, fmt.Errorf("validate params failed for %s: %w", propertyName, err)
		}

		s.logger.WithContext(ctx).Debugf("[KnLogicPropertyResolver] Validation passed for %s", propertyName)
	}

	return dynamicParams, missingParams, nil
}

// generateMetricParams generates dynamic parameters of metric type through Agent.
// Note: This method encapsulates Agent calls, and can be expanded to support direct calls to LLM in the future.
func (s *knLogicPropertyResolverService) generateMetricParams(
	ctx context.Context,
	req *interfaces.ResolveLogicPropertiesRequest,
	property *interfaces.LogicPropertyDef,
	propertyName string,
	debugCollector *DebugCollector,
) (dynamicParams map[string]any, missingParams *interfaces.MissingPropertyParams, err error) {
	s.logger.WithContext(ctx).Debugf("[KnLogicPropertyResolver] Generating metric params via Agent for: %s", property.Name)

	// Generate now_ms if the caller does not provide it in additional_context.
	nowMs := time.Now().UnixMilli()

	// Build the Agent request.
	agentReq := &interfaces.MetricDynamicParamsGeneratorReq{
		LogicProperty:     property,
		Query:             req.Query,
		UniqueIdentities:  req.InstanceIdentities,
		AdditionalContext: req.AdditionalContext,
		NowMs:             nowMs,
		Timezone:          "", // Ignore timezone for now.
	}

	// Log Agent request information.
	if debugCollector != nil {
		debugCollector.RecordMetricAgentRequest(propertyName, agentReq)
	}

	// Directly connected to LLM to generate metric dynamic parameters (replacing agent-factory agent); req.LLMModel is the default large model of the idling system.
	agentResult, missingParams, err := s.dynamicLLM.GenerateMetricParams(ctx, agentReq, req.LLMModel)
	if err != nil {
		return nil, nil, err
	}

	// Return directly when required parameters are missing.
	if missingParams != nil {
		return nil, missingParams, nil
	}

	// Extract the parameter object for the corresponding property from the Agent result.
	// Agent return format: {"approved_drug_count": {"instant": false, "start": xxx, ...}}
	// We need to extract: {"instant": false, "start": xxx, ...}
	if agentResult != nil {
		if propertyParams, ok := agentResult[property.Name]; ok {
			if paramsMap, ok := propertyParams.(map[string]any); ok {
				return paramsMap, nil, nil
			}
		}
		// Return an error if extraction fails.
		return nil, nil, fmt.Errorf("failed to extract params for property %s from agent result: %+v", property.Name, agentResult)
	}

	return nil, nil, nil
}

// generateToolParams generates dynamic parameters for ToolBox-backed logical properties.
func (s *knLogicPropertyResolverService) generateToolParams(
	ctx context.Context,
	req *interfaces.ResolveLogicPropertiesRequest,
	property *interfaces.LogicPropertyDef,
	propertyName string,
	debugCollector *DebugCollector,
) (dynamicParams map[string]any, missingParams *interfaces.MissingPropertyParams, err error) {
	s.logger.WithContext(ctx).Debugf("[KnLogicPropertyResolver] Generating tool parameters for: %s", property.Name)

	var boxID, toolID string
	if property.DataSource != nil {
		boxID, _ = property.DataSource["box_id"].(string)
		toolID, _ = property.DataSource["tool_id"].(string)
	}

	// Build the Agent request.
	agentReq := &interfaces.ToolDynamicParamsGeneratorReq{
		BoxID:             boxID,
		ToolID:            toolID,
		LogicProperty:     property,
		Query:             req.Query,
		UniqueIdentities:  req.InstanceIdentities,
		AdditionalContext: req.AdditionalContext,
	}

	// Log Agent request information.
	if debugCollector != nil {
		debugCollector.RecordToolAgentRequest(propertyName, agentReq)
	}

	agentResult, missingParams, err := s.dynamicLLM.GenerateToolParams(ctx, agentReq, req.LLMModel)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("[KnLogicPropertyResolver] GenerateToolParams failed: %v", err)
		return nil, nil, err
	}

	// Return directly when required parameters are missing.
	if missingParams != nil {
		return nil, missingParams, nil
	}

	// Extract the parameter object for the corresponding property from the Agent result.
	if agentResult != nil {
		if propertyParams, ok := agentResult[property.Name]; ok {
			if paramsMap, ok := propertyParams.(map[string]any); ok {
				return paramsMap, nil, nil
			}
		}
		// Return an error if extraction fails.
		return nil, nil, fmt.Errorf("failed to extract params for property %s from agent result: %+v", property.Name, agentResult)
	}

	return nil, nil, nil
}

// validateMetricParams validates parameters of metric type.
func (s *knLogicPropertyResolverService) validateMetricParams(
	ctx context.Context,
	property *interfaces.LogicPropertyDef,
	params map[string]any,
) error {
	// 1. Check the instant field (required)
	instantVal, hasInstant := params["instant"]
	if !hasInstant {
		// 🔧 Temporary solution: If instant is missing, it will be automatically inferred based on whether there is step.
		_, hasStep := params["step"]
		if hasStep {
			// There is a step indicating that it is a trend query.
			params["instant"] = false
			s.logger.WithContext(ctx).Warnf("[KnLogicPropertyResolver] Auto-inferred instant=false for metric property: %s (has step field)", property.Name)
			instantVal = false
		} else {
			// There is no step indicating that it is an immediate query.
			params["instant"] = true
			s.logger.WithContext(ctx).Warnf("[KnLogicPropertyResolver] Auto-inferred instant=true for metric property: %s (no step field)", property.Name)
			instantVal = true
		}
	}

	instant, ok := instantVal.(bool)
	if !ok {
		return fmt.Errorf("param 'instant' must be boolean for metric property: %s", property.Name)
	}

	// 2. Check start and end (usually required)
	if _, hasStart := params["start"]; !hasStart {
		return fmt.Errorf("missing required param 'start' for metric property: %s", property.Name)
	}
	if _, hasEnd := params["end"]; !hasEnd {
		return fmt.Errorf("missing required param 'end' for metric property: %s", property.Name)
	}

	// 3. check step field.
	stepVal, hasStep := params["step"]

	// When instant=true, there should be no step.
	if instant && hasStep {
		return fmt.Errorf("metric property %s: instant=true cannot have 'step' field", property.Name)
	}

	// When instant=false, there must be step.
	if !instant && !hasStep {
		return fmt.Errorf("metric property %s: instant=false must have 'step' field", property.Name)
	}

	// 4. If there is a step, verify the enumeration value.
	if hasStep {
		step, ok := stepVal.(string)
		if !ok {
			return fmt.Errorf("param 'step' must be string for metric property: %s", property.Name)
		}

		validSteps := []string{"day", "week", "month", "quarter", "year"}
		isValid := false
		for _, validStep := range validSteps {
			if step == validStep {
				isValid = true
				break
			}
		}

		if !isValid {
			return fmt.Errorf("metric property %s: invalid step value '%s', must be one of: day, week, month, quarter, year",
				property.Name, step)
		}
	}

	// 5. Verify that start and end are numeric types (timestamps)
	if err := s.validateTimestamp(ctx, params["start"], "start", property.Name); err != nil {
		return err
	}
	if err := s.validateTimestamp(ctx, params["end"], "end", property.Name); err != nil {
		return err
	}

	s.logger.WithContext(ctx).Debugf("[KnLogicPropertyResolver] Metric params validation passed for: %s", property.Name)
	return nil
}

// validateTimestamp validate timestamp parameter.
func (s *knLogicPropertyResolverService) validateTimestamp(
	_ context.Context,
	value interface{},
	paramName, propertyName string,
) error {
	switch v := value.(type) {
	case int64:
		// Verification timestamp range (millisecond level, roughly between 2000-2100)
		if v < 946684800000 || v > 4102444800000 {
			return fmt.Errorf("metric property %s: param '%s' timestamp %d is out of reasonable range",
				propertyName, paramName, v)
		}
		return nil
	case json.Number:
		// UseNumber decoders hand back json.Number; see drivenadapters.precisionJSON.
		timestamp, convErr := v.Int64()
		if convErr != nil {
			return fmt.Errorf("metric property %s: param '%s' timestamp %s is not an integer",
				propertyName, paramName, v.String())
		}
		if timestamp < 946684800000 || timestamp > 4102444800000 {
			return fmt.Errorf("metric property %s: param '%s' timestamp %d is out of reasonable range",
				propertyName, paramName, timestamp)
		}
		return nil
	case float64:
		// JSON parsing may parse numbers as float64.
		timestamp := int64(v)
		if timestamp < 946684800000 || timestamp > 4102444800000 {
			return fmt.Errorf("metric property %s: param '%s' timestamp %d is out of reasonable range",
				propertyName, paramName, timestamp)
		}
		return nil
	case int:
		timestamp := int64(v)
		if timestamp < 946684800000 || timestamp > 4102444800000 {
			return fmt.Errorf("metric property %s: param '%s' timestamp %d is out of reasonable range",
				propertyName, paramName, timestamp)
		}
		return nil
	default:
		return fmt.Errorf("metric property %s: param '%s' must be a number (int64 timestamp), got %T",
			propertyName, paramName, value)
	}
}

// validateToolParams validates ToolBox dynamic parameters.
func (s *knLogicPropertyResolverService) validateToolParams(
	ctx context.Context,
	_ *interfaces.LogicPropertyDef,
	_ map[string]interface{},
) error {
	s.logger.WithContext(ctx).Debugf("[KnLogicPropertyResolver] Tool parameter validation passed")
	return nil
}

// queryLogicProperties calls ontology-query to query logical property values.
func (s *knLogicPropertyResolverService) queryLogicProperties(
	ctx context.Context,
	req *interfaces.ResolveLogicPropertiesRequest,
	dynamicParams map[string]interface{},
) ([]map[string]interface{}, error) {
	// Buildqueryrequest.
	queryReq := &interfaces.QueryLogicPropertiesReq{
		KnID:               req.KnID,
		OtID:               req.OtID,
		InstanceIdentities: req.InstanceIdentities,
		Properties:         req.Properties,
		DynamicParams:      dynamicParams,
	}

	// Call ontology-query service.
	resp, err := s.ontologyQueryClient.QueryLogicProperties(ctx, queryReq)
	if err != nil {
		return nil, errors.DefaultHTTPError(ctx, http.StatusInternalServerError,
			fmt.Sprintf("query logic properties failed: %v", err))
	}

	return resp.Datas, nil
}

// buildMissingParamsError build missing parameters error.
func (s *knLogicPropertyResolverService) buildMissingParamsError(
	ctx context.Context,
	missingParams []interfaces.MissingPropertyParams,
	debugInfo *interfaces.ResolveDebugInfo,
) error {
	// Build error message (for ErrorMsg field)
	errorMsg := ""
	for i, mp := range missingParams {
		if i > 0 {
			errorMsg += "; "
		}
		if mp.ErrorMsg != "" {
			errorMsg += fmt.Sprintf("missing %s: %s", mp.Property, mp.ErrorMsg)
		} else {
			errorMsg += fmt.Sprintf("missing %s", mp.Property)
		}
	}

	missingError := &interfaces.MissingParamsError{
		ErrorCode: "MISSING_INPUT_PARAMS",
		Message:   errors.LocalizedDetail(ctx, "LogicPropertyInputMissing"),
		ErrorMsg:  errorMsg,
		Debug:     debugInfo,
		TraceID:   "",
		Missing:   missingParams,
	}

	// Returned as HTTPError.
	return errors.DefaultHTTPError(ctx, http.StatusBadRequest, fmt.Sprintf("%+v", missingError))
}

// buildGenerationFailedError Build parameter generation failed error. Distinguish from missing parameters: here is LLM or dependent service.
// The fault is a server-side problem (500). The error message is retained in the upstream code/detail for easy location.
func (s *knLogicPropertyResolverService) buildGenerationFailedError(
	ctx context.Context,
	failures []interfaces.MissingPropertyParams,
) error {
	errorMsg := ""
	for i, f := range failures {
		if i > 0 {
			errorMsg += "; "
		}
		errorMsg += fmt.Sprintf("%s: %s", f.Property, f.ErrorMsg)
	}

	return errors.DefaultHTTPError(ctx, http.StatusInternalServerError,
		fmt.Sprintf("DYNAMIC_PARAMS_GENERATION_FAILED: %s: %s",
			errors.LocalizedDetail(ctx, "LogicPropertyGenerationFailed"), errorMsg))
}
