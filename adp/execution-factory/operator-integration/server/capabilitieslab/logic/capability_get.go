// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logic

import (
	"context"
	"fmt"

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/capabilitieslab/client"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/capabilitieslab/model"
)

func (s *Service) GetCapability(
	ctx context.Context,
	businessDomain, capabilityID string,
) (*model.Capability, error) {
	kind := ParseCapabilityKind(capabilityID)

	switch kind {
	case "http":
		return s.getHttpCapability(ctx, businessDomain, capabilityID)
	case "function":
		return s.getFunctionCapability(ctx, businessDomain, capabilityID)
	case "mcp":
		return s.getMcpCapability(ctx, businessDomain, capabilityID)
	case "skill":
		return s.getSkillCapability(ctx, businessDomain, capabilityID)
	default:
		return nil, fmt.Errorf("invalid capability id")
	}
}

func (s *Service) getHttpCapability(ctx context.Context, businessDomain, capabilityID string) (*model.Capability, error) {
	boxID, toolID, ok := ParseHttpCapabilityID(capabilityID)
	if !ok {
		return nil, fmt.Errorf("invalid http capability id")
	}

	box, err := s.findToolbox(ctx, businessDomain, boxID)
	if err != nil {
		return nil, err
	}

	tool, err := s.Client.GetTool(ctx, businessDomain, boxID, toolID)
	if err != nil {
		return nil, err
	}

	capability := &model.Capability{
		ID:          capabilityID,
		Kind:        "http",
		Name:        tool.Name,
		Description: tool.Description,
		Status:      mapToolStatus(tool.Status, box.Status),
		Group: &model.Group{
			ID:         box.BoxID,
			Name:       box.BoxName,
			ServiceURL: box.BoxSvcURL,
			Status:     box.Status,
			Category:   box.BoxCategory,
		},
		UpdateTime: tool.UpdateTime,
		Audit:      auditFromToolDetail(tool),
		ToolID:     tool.ToolID,
		BoxID:      box.BoxID,
	}
	if tool.Metadata != nil {
		capability.Version = tool.Metadata.Version
	}

	if ep := endpointFromToolMetadata(tool); ep != nil {
		capability.Endpoint = &model.Endpoint{Method: ep.Method, Path: ep.Path}
	} else if tool.Metadata != nil && len(tool.Metadata.APISpec) > 0 {
		capability.Endpoint = extractEndpoint(string(tool.Metadata.APISpec))
	}

	capability.Orchestration = s.resolveOrchestrationForTool(ctx, businessDomain, boxID, client.ToolInfo{
		ToolID:         tool.ToolID,
		Name:           tool.Name,
		Description:    tool.Description,
		Status:         tool.Status,
		CreateUser:     tool.CreateUser,
		CreateTime:     tool.CreateTime,
		UpdateUser:     tool.UpdateUser,
		UpdateTime:     tool.UpdateTime,
		ReleaseUser:    tool.ReleaseUser,
		ReleaseTime:    tool.ReleaseTime,
		SourceID:       tool.SourceID,
		SourceType:     tool.SourceType,
		ResourceObject: tool.ResourceObject,
	})
	s.enrichOrchestrationAudit(ctx, businessDomain, capability.Orchestration)
	capability.OpenAPISpec = openAPISpecFromToolMetadata(tool)
	return capability, nil
}

func (s *Service) getFunctionCapability(ctx context.Context, businessDomain, capabilityID string) (*model.Capability, error) {
	boxID, toolID, ok := ParseFunctionCapabilityID(capabilityID)
	if !ok {
		return nil, fmt.Errorf("invalid function capability id")
	}

	box, err := s.findToolbox(ctx, businessDomain, boxID)
	if err != nil {
		return nil, err
	}

	tool, err := s.Client.GetTool(ctx, businessDomain, boxID, toolID)
	if err != nil {
		return nil, err
	}

	capability := &model.Capability{
		ID:          capabilityID,
		Kind:        "function",
		Name:        tool.Name,
		Description: tool.Description,
		Status:      mapToolStatus(tool.Status, box.Status),
		Group: &model.Group{
			ID:         box.BoxID,
			Name:       box.BoxName,
			ServiceURL: box.BoxSvcURL,
			Status:     box.Status,
			Category:   box.BoxCategory,
		},
		UpdateTime: tool.UpdateTime,
		Audit:      auditFromToolDetail(tool),
		ToolID:     tool.ToolID,
		BoxID:      box.BoxID,
	}
	if tool.Metadata != nil {
		capability.Version = tool.Metadata.Version
	}
	functionMetadata := functionMetadataFromTool(tool)
	capability.Code = functionMetadata.Code
	capability.Inputs = functionMetadata.Inputs
	capability.Outputs = functionMetadata.Outputs

	return capability, nil
}

func (s *Service) getMcpCapability(ctx context.Context, businessDomain, capabilityID string) (*model.Capability, error) {
	mcpID, ok := ParseMcpCapabilityID(capabilityID)
	if !ok {
		return nil, fmt.Errorf("invalid mcp capability id")
	}

	resp, err := s.Client.ListMcps(ctx, businessDomain, "", 1, 100)
	if err != nil {
		return nil, err
	}

	for _, item := range resp.Data {
		if item.McpID == mcpID {
			capability := &model.Capability{
				ID:          capabilityID,
				Kind:        "mcp",
				Name:        item.Name,
				Description: item.Description,
				Status:      mapMcpSkillStatus(item.Status),
				UpdateTime:  item.UpdateTime,
				Audit:       auditFromMcp(item),
				McpID:       item.McpID,
			}
			if detail, detailErr := s.Client.GetMcp(ctx, businessDomain, mcpID); detailErr == nil && detail != nil {
				capability.URL = detail.URL
			}
			return capability, nil
		}
	}

	return nil, fmt.Errorf("mcp capability not found")
}

func (s *Service) getSkillCapability(ctx context.Context, businessDomain, capabilityID string) (*model.Capability, error) {
	skillID, ok := ParseSkillCapabilityID(capabilityID)
	if !ok {
		return nil, fmt.Errorf("invalid skill capability id")
	}

	skill, err := s.Client.GetSkill(ctx, businessDomain, skillID)
	if err != nil {
		return nil, err
	}

	return &model.Capability{
		ID:          capabilityID,
		Kind:        "skill",
		Name:        skill.Name,
		Description: skill.Description,
		Status:      mapMcpSkillStatus(skill.Status),
		UpdateTime:  skill.UpdateTime,
		Audit:       auditFromSkillDetail(skill),
		SkillID:     skill.SkillID,
		Version:     skill.Version,
	}, nil
}

func (s *Service) GetOrchestrationDetail(
	ctx context.Context,
	businessDomain, capabilityID string,
) (*model.OrchestrationDetailResponse, error) {
	capability, err := s.GetCapability(ctx, businessDomain, capabilityID)
	if err != nil {
		return nil, err
	}

	if capability.Kind != "http" {
		return &model.OrchestrationDetailResponse{Enabled: false}, nil
	}

	if capability.Orchestration != nil && capability.Orchestration.Enabled {
		return &model.OrchestrationDetailResponse{
			Enabled:    true,
			OperatorID: capability.Orchestration.OperatorID,
			ToolID:     capability.ToolID,
			BoxID:      capability.BoxID,
			Audit:      capability.Orchestration.Audit,
		}, nil
	}

	return &model.OrchestrationDetailResponse{
		Enabled: false,
		ToolID:  capability.ToolID,
		BoxID:   capability.BoxID,
	}, nil
}
