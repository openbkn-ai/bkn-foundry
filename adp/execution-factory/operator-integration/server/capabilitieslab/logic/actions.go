// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logic

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/capabilitieslab/client"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/capabilitieslab/model"
)

func (s *Service) ImportOpenApiCapabilities(
	ctx context.Context,
	businessDomain string,
	req model.ImportOpenApiCapabilityRequest,
) (*model.ImportOpenApiCapabilityResponse, error) {
	createReq := model.CreateHttpCapabilityRequest{
		OpenAPISpec:          req.OpenAPISpec,
		ServiceURL:           req.ServiceURL,
		Description:          req.Description,
		Category:             req.Category,
		Group:                req.Group,
		OrchestrationEnabled: req.OrchestrationEnabled,
	}
	if createReq.Group.Mode == "" {
		createReq.Group.Mode = "auto"
	}

	groupName, boxID, err := s.resolveGroup(ctx, businessDomain, createReq)
	if err != nil {
		return nil, err
	}

	category := req.Category
	if category == "" {
		category = "other_category"
	}

	if req.OrchestrationEnabled {
		bundleReq := client.BundleRequestFromModel(boxID, groupName, struct {
			OpenAPISpec string
			ServiceURL  string
			Description string
			Category    string
		}{
			OpenAPISpec: req.OpenAPISpec,
			ServiceURL:  req.ServiceURL,
			Description: req.Description,
			Category:    category,
		}, category)

		bundle, bundleErr := s.Client.RegisterOpenAPIBundle(ctx, businessDomain, bundleReq)
		if bundleErr != nil {
			return nil, bundleErr
		}

		response := &model.ImportOpenApiCapabilityResponse{
			BoxID:        bundle.BoxID,
			FailureCount: bundle.FailureCount,
			Failures:     bundle.Failures,
		}

		for i, toolID := range bundle.ToolIDs {
			capability, capErr := s.GetCapability(ctx, businessDomain, BuildHttpCapabilityID(bundle.BoxID, toolID))
			if capErr != nil {
				continue
			}
			response.Capabilities = append(response.Capabilities, *capability)
			if i < len(bundle.Links) {
				response.Links = append(response.Links, model.Link{
					OperatorID: bundle.Links[i].OperatorID,
					ToolID:     bundle.Links[i].ToolID,
				})
			}
		}

		if len(response.Capabilities) == 0 {
			return nil, errors.New("no capabilities imported from openapi")
		}

		return response, nil
	}

	if boxID == "" {
		created, createErr := s.Client.CreateToolbox(ctx, businessDomain, client.CreateToolboxPayload(
			groupName, req.Description, req.ServiceURL, category,
		))
		if createErr != nil {
			return nil, createErr
		}
		boxID = created.BoxID
	}

	toolPayload, payloadErr := client.CreateToolPayload(req.OpenAPISpec)
	if payloadErr != nil {
		return nil, payloadErr
	}

	toolResp, toolErr := s.Client.CreateTool(ctx, businessDomain, boxID, toolPayload)
	if toolErr != nil {
		return nil, toolErr
	}

	response := &model.ImportOpenApiCapabilityResponse{BoxID: boxID}
	for _, toolID := range toolResp.SuccessIDs {
		capability, capErr := s.GetCapability(ctx, businessDomain, BuildHttpCapabilityID(boxID, toolID))
		if capErr != nil {
			continue
		}
		response.Capabilities = append(response.Capabilities, *capability)
	}

	if len(response.Capabilities) == 0 {
		if toolResp.FailureCount > 0 && len(toolResp.Failures) > 0 && toolResp.Failures[0].Message() != "" {
			return nil, errors.New(toolResp.Failures[0].Message())
		}
		return nil, errors.New("import failed")
	}

	return response, nil
}

func (s *Service) DebugCapability(
	ctx context.Context,
	businessDomain, capabilityID string,
	req model.DebugCapabilityRequest,
) (*model.DebugCapabilityResponse, error) {
	kind := ParseCapabilityKind(capabilityID)

	switch kind {
	case "http", "function":
		boxID, toolID, ok := parseToolCapabilityID(capabilityID)
		if !ok {
			return nil, errors.New("invalid capability id")
		}

		resp, err := s.Client.DebugTool(ctx, businessDomain, boxID, toolID, client.DebugToolRequest{
			Body:    req.Body,
			Query:   req.Query,
			Path:    req.Path,
			Header:  req.Header,
			Timeout: req.Timeout,
		})
		if err != nil {
			return nil, err
		}

		var body interface{}
		if len(resp.Body) > 0 {
			_ = json.Unmarshal(resp.Body, &body)
		}

		return &model.DebugCapabilityResponse{
			StatusCode: resp.StatusCode,
			Body:       body,
			DurationMs: resp.DurationMs,
			Error:      resp.Error,
		}, nil

	case "mcp":
		mcpID, ok := ParseMcpCapabilityID(capabilityID)
		if !ok {
			return nil, errors.New("invalid mcp capability id")
		}
		if req.ToolName == "" {
			return nil, errors.New("tool_name is required for mcp debug")
		}

		resp, err := s.Client.DebugMcpTool(ctx, businessDomain, mcpID, req.ToolName, req.Body)
		if err != nil {
			return nil, err
		}

		return &model.DebugCapabilityResponse{
			Content: resp.Content,
			IsError: resp.IsError,
		}, nil

	default:
		return nil, errors.New("debug not supported for this capability kind")
	}
}

func parseToolCapabilityID(capabilityID string) (boxID, toolID string, ok bool) {
	if boxID, toolID, ok = ParseHttpCapabilityID(capabilityID); ok {
		return boxID, toolID, true
	}
	return ParseFunctionCapabilityID(capabilityID)
}

func (s *Service) ListVersions(
	ctx context.Context,
	businessDomain, capabilityID string,
) (*model.VersionListResponse, error) {
	capability, err := s.GetCapability(ctx, businessDomain, capabilityID)
	if err != nil {
		return nil, err
	}

	switch capability.Kind {
	case "skill":
		history, histErr := s.Client.GetSkillHistory(ctx, businessDomain, capability.SkillID)
		if histErr != nil {
			return nil, histErr
		}

		versions := make([]model.VersionEntry, 0, len(history))
		for _, item := range history {
			versions = append(versions, model.VersionEntry{
				Version:     item.Version,
				Status:      item.Status,
				ReleaseUser: item.ReleaseUser,
				ReleaseTime: item.ReleaseTime,
			})
		}
		versions = ensureCurrentVersionEntry(capability, versions)

		return &model.VersionListResponse{Kind: "skill", Versions: versions}, nil

	case "http":
		if capability.Orchestration == nil || !capability.Orchestration.Enabled {
			return &model.VersionListResponse{
				Kind:     "http",
				Versions: ensureCurrentVersionEntry(capability, nil),
			}, nil
		}

		history, histErr := s.Client.GetOperatorHistory(ctx, businessDomain, capability.Orchestration.OperatorID)
		if histErr != nil {
			return nil, histErr
		}

		versions := make([]model.VersionEntry, 0, len(history))
		for _, item := range history {
			versions = append(versions, model.VersionEntry{
				Version:     item.Version,
				Status:      item.Status,
				ReleaseUser: item.ReleaseUser,
				ReleaseTime: item.ReleaseTime,
				UpdateTime:  item.UpdateTime,
			})
		}
		versions = ensureCurrentVersionEntry(capability, versions)

		return &model.VersionListResponse{Kind: "http", Versions: versions}, nil

	default:
		return &model.VersionListResponse{
			Kind:     capability.Kind,
			Versions: ensureCurrentVersionEntry(capability, nil),
		}, nil
	}
}

func ensureCurrentVersionEntry(capability *model.Capability, versions []model.VersionEntry) []model.VersionEntry {
	if capability == nil || capability.Version == "" {
		return versions
	}

	for _, item := range versions {
		if item.Version == capability.Version {
			return versions
		}
	}

	return append([]model.VersionEntry{{
		Version:    capability.Version,
		Status:     capability.Status,
		UpdateTime: capability.UpdateTime,
	}}, versions...)
}

func (s *Service) RepublishVersion(
	ctx context.Context,
	businessDomain, capabilityID string,
	req model.RepublishVersionRequest,
) error {
	capability, err := s.GetCapability(ctx, businessDomain, capabilityID)
	if err != nil {
		return err
	}

	mode := strings.ToLower(req.Mode)
	switch capability.Kind {
	case "skill":
		if mode == "publish" {
			return s.Client.PublishSkillHistory(ctx, businessDomain, capability.SkillID, req.Version)
		}
		return s.Client.RepublishSkillHistory(ctx, businessDomain, capability.SkillID, req.Version)
	case "http":
		if capability.Orchestration == nil || !capability.Orchestration.Enabled || capability.Orchestration.OperatorID == "" {
			return errors.New("historical version restore is only available after orchestration is enabled")
		}
		return s.Client.UpdateOperatorStatus(ctx, businessDomain, s.DefaultUserID, capability.Orchestration.OperatorID, "published", req.Version)
	default:
		return errors.New("republish not supported for this capability kind")
	}
}

func (s *Service) PublishCapability(
	ctx context.Context,
	businessDomain, capabilityID string,
	status string,
) error {
	capability, err := s.GetCapability(ctx, businessDomain, capabilityID)
	if err != nil {
		return err
	}

	switch capability.Kind {
	case "http", "function":
		if capability.BoxID == "" {
			return errors.New("missing group for capability")
		}
		return s.Client.UpdateToolboxStatus(ctx, businessDomain, capability.BoxID, status)
	case "skill":
		return s.Client.UpdateSkillStatus(ctx, businessDomain, capability.SkillID, mapSkillPublishStatus(status))
	case "mcp":
		return s.Client.UpdateMcpStatus(ctx, businessDomain, capability.McpID, mapMcpPublishStatus(status))
	default:
		return errors.New("publish not supported for this capability kind")
	}
}

func mapSkillPublishStatus(status string) string {
	switch status {
	case "published":
		return "published"
	case "offline":
		return "offline"
	default:
		return "unpublish"
	}
}

func mapMcpPublishStatus(status string) string {
	switch status {
	case "published":
		return "published"
	default:
		return "unpublish"
	}
}

func (s *Service) EnableOrchestration(
	ctx context.Context,
	businessDomain, capabilityID string,
	req model.EnableOrchestrationRequest,
) (*model.EnableOrchestrationResponse, error) {
	capability, err := s.GetCapability(ctx, businessDomain, capabilityID)
	if err != nil {
		return nil, err
	}

	if capability.Kind != "http" {
		return nil, errors.New("orchestration only supported for http capabilities")
	}

	if capability.Orchestration != nil && capability.Orchestration.Enabled {
		return &model.EnableOrchestrationResponse{
			OperatorID: capability.Orchestration.OperatorID,
			Audit:      capability.Orchestration.Audit,
		}, nil
	}

	tool, err := s.Client.GetTool(ctx, businessDomain, capability.BoxID, capability.ToolID)
	if err != nil {
		return nil, err
	}

	openapiSpec := openAPISpecFromToolMetadata(tool)
	if openapiSpec == "" {
		return nil, errors.New("tool has no openapi metadata")
	}

	ids, regErr := s.Client.RegisterOperatorOpenAPI(
		ctx,
		businessDomain,
		openapiSpec,
		operatorInfoForCapability(capability),
		operatorExecuteControlToMap(req.OperatorExecuteControl),
		true,
	)
	if regErr != nil {
		return nil, regErr
	}
	if len(ids) == 0 {
		return nil, errors.New("operator registration failed")
	}

	operator, _ := s.Client.GetOperator(ctx, businessDomain, ids[0])
	return &model.EnableOrchestrationResponse{
		OperatorID: ids[0],
		Audit:      auditFromOperator(operator),
	}, nil
}

func operatorInfoForCapability(capability *model.Capability) map[string]interface{} {
	category := "other_category"
	if capability != nil && capability.Group != nil && capability.Group.Category != "" {
		category = capability.Group.Category
	}

	return map[string]interface{}{
		"operator_type":  "basic",
		"execution_mode": "sync",
		"category":       category,
		"source":         "custom",
		"is_data_source": false,
	}
}

func operatorExecuteControlToMap(control model.OperatorExecuteControl) map[string]interface{} {
	retryPolicy := control.RetryPolicy
	retryConditions := retryPolicy.RetryConditions

	executeControl := map[string]interface{}{}
	if control.Timeout > 0 {
		executeControl["timeout"] = control.Timeout
	}

	retryPolicyMap := map[string]interface{}{}
	if retryPolicy.MaxAttempts > 0 {
		retryPolicyMap["max_attempts"] = retryPolicy.MaxAttempts
	}
	if retryPolicy.InitialDelay > 0 {
		retryPolicyMap["initial_delay"] = retryPolicy.InitialDelay
	}
	if retryPolicy.MaxDelay > 0 {
		retryPolicyMap["max_delay"] = retryPolicy.MaxDelay
	}
	if retryPolicy.BackoffFactor > 0 {
		retryPolicyMap["backoff_factor"] = retryPolicy.BackoffFactor
	}

	retryConditionsMap := map[string]interface{}{}
	if len(retryConditions.StatusCode) > 0 {
		retryConditionsMap["status_code"] = retryConditions.StatusCode
	}
	if len(retryConditions.ErrorCodes) > 0 {
		retryConditionsMap["error_codes"] = retryConditions.ErrorCodes
	}
	if len(retryConditionsMap) > 0 {
		retryPolicyMap["retry_conditions"] = retryConditionsMap
	}
	if len(retryPolicyMap) > 0 {
		executeControl["retry_policy"] = retryPolicyMap
	}

	if len(executeControl) == 0 {
		return nil
	}
	return executeControl
}

func (s *Service) DisableOrchestration(
	ctx context.Context,
	businessDomain, capabilityID string,
) (*model.DisableOrchestrationResponse, error) {
	capability, err := s.GetCapability(ctx, businessDomain, capabilityID)
	if err != nil {
		return nil, err
	}

	if capability.Kind != "http" {
		return nil, errors.New("orchestration only supported for http capabilities")
	}

	if capability.Orchestration == nil || capability.Orchestration.OperatorID == "" {
		return &model.DisableOrchestrationResponse{Enabled: false}, nil
	}

	if err := s.Client.UpdateOperatorStatus(ctx, businessDomain, s.DefaultUserID, capability.Orchestration.OperatorID, "offline"); err != nil {
		return nil, err
	}

	return &model.DisableOrchestrationResponse{
		Enabled:    false,
		OperatorID: capability.Orchestration.OperatorID,
	}, nil
}

func (s *Service) UpdateOrchestrationConfig(
	ctx context.Context,
	businessDomain, capabilityID string,
	req model.UpdateOrchestrationConfigRequest,
) (*model.OrchestrationDetailResponse, error) {
	capability, err := s.GetCapability(ctx, businessDomain, capabilityID)
	if err != nil {
		return nil, err
	}

	if capability.Kind != "http" {
		return nil, errors.New("orchestration only supported for http capabilities")
	}

	if capability.Orchestration == nil || !capability.Orchestration.Enabled || capability.Orchestration.OperatorID == "" {
		return nil, errors.New("enable orchestration before saving operator settings")
	}

	tool, err := s.Client.GetTool(ctx, businessDomain, capability.BoxID, capability.ToolID)
	if err != nil {
		return nil, err
	}

	openapiSpec := openAPISpecFromToolMetadata(tool)
	if openapiSpec == "" {
		return nil, errors.New("tool has no openapi metadata")
	}

	var openapiData interface{} = openapiSpec
	var parsedOpenAPI map[string]interface{}
	if err := json.Unmarshal([]byte(openapiSpec), &parsedOpenAPI); err == nil && parsedOpenAPI != nil {
		openapiData = parsedOpenAPI
	}

	if err := s.Client.UpdateOperatorConfig(
		ctx,
		businessDomain,
		s.DefaultUserID,
		capability.Orchestration.OperatorID,
		capability.Name,
		capability.Description,
		"openapi",
		openapiData,
		operatorInfoForCapability(capability),
		operatorExecuteControlToMap(req.OperatorExecuteControl),
	); err != nil {
		return nil, err
	}
	if err := s.Client.UpdateOperatorStatus(ctx, businessDomain, s.DefaultUserID, capability.Orchestration.OperatorID, "published"); err != nil {
		return nil, err
	}

	operator, _ := s.Client.GetOperator(ctx, businessDomain, capability.Orchestration.OperatorID)
	return &model.OrchestrationDetailResponse{
		Enabled:    true,
		OperatorID: capability.Orchestration.OperatorID,
		ToolID:     capability.ToolID,
		BoxID:      capability.BoxID,
		Audit:      auditFromOperator(operator),
	}, nil
}

func (s *Service) PublishGroup(ctx context.Context, businessDomain, groupID, status string) error {
	return s.Client.UpdateToolboxStatus(ctx, businessDomain, groupID, status)
}
