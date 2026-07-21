// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logic

import (
	"context"
	"errors"
	"fmt"

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/capabilitieslab/client"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/capabilitieslab/model"
)

func (s *Service) ListCategories(ctx context.Context, businessDomain string) ([]client.CategoryEntry, error) {
	return s.Client.ListCategories(ctx, businessDomain)
}

func (s *Service) UpdateCapability(
	ctx context.Context,
	businessDomain, capabilityID string,
	req model.UpdateCapabilityRequest,
) (*model.Capability, error) {
	capability, err := s.GetCapability(ctx, businessDomain, capabilityID)
	if err != nil {
		return nil, err
	}

	switch capability.Kind {
	case "http":
		return s.UpdateHttpCapability(ctx, businessDomain, capabilityID, model.UpdateHttpCapabilityRequest{
			Name:        req.Name,
			Description: req.Description,
			OpenAPISpec: req.OpenAPISpec,
		})
	case "mcp":
		return s.updateMcpCapability(ctx, businessDomain, capability, req)
	case "skill":
		return s.updateSkillMetadataCapability(ctx, businessDomain, capability, req)
	case "function":
		return s.updateFunctionCapability(ctx, businessDomain, capability, req)
	default:
		return nil, fmt.Errorf("update not supported for %s capabilities", capability.Kind)
	}
}

func (s *Service) updateMcpCapability(
	ctx context.Context,
	businessDomain string,
	capability *model.Capability,
	req model.UpdateCapabilityRequest,
) (*model.Capability, error) {
	if capability.McpID == "" {
		return nil, errors.New("missing mcp id")
	}

	body := map[string]interface{}{}
	if req.Name != "" {
		body["name"] = req.Name
	}
	if req.Description != "" {
		body["description"] = req.Description
	}
	if req.URL != "" {
		body["url"] = req.URL
	}
	if req.Mode != "" {
		body["mode"] = req.Mode
	}
	if req.Headers != nil {
		body["headers"] = req.Headers
	}
	if req.Category != "" {
		body["category"] = req.Category
	}
	if req.CreationType != "" {
		body["creation_type"] = req.CreationType
	}

	if len(body) == 0 {
		return capability, nil
	}

	if err := s.Client.UpdateMcp(ctx, businessDomain, capability.McpID, body); err != nil {
		return nil, err
	}

	return s.GetCapability(ctx, businessDomain, capability.ID)
}

func (s *Service) updateSkillMetadataCapability(
	ctx context.Context,
	businessDomain string,
	capability *model.Capability,
	req model.UpdateCapabilityRequest,
) (*model.Capability, error) {
	if capability.SkillID == "" {
		return nil, errors.New("missing skill id")
	}

	body := map[string]interface{}{}
	if req.Name != "" {
		body["name"] = req.Name
	}
	if req.Description != "" {
		body["description"] = req.Description
	}
	if req.Category != "" {
		body["category"] = req.Category
	} else {
		body["category"] = "other_category"
	}
	if req.Source != "" {
		body["source"] = req.Source
	}

	if len(body) == 0 {
		return capability, nil
	}

	if err := s.Client.UpdateSkillMetadata(ctx, businessDomain, capability.SkillID, body); err != nil {
		return nil, err
	}

	return s.GetCapability(ctx, businessDomain, capability.ID)
}

func (s *Service) updateFunctionCapability(
	ctx context.Context,
	businessDomain string,
	capability *model.Capability,
	req model.UpdateCapabilityRequest,
) (*model.Capability, error) {
	if capability.BoxID == "" || capability.ToolID == "" {
		return nil, errors.New("missing tool reference")
	}

	functionInput := map[string]interface{}{
		"script_type": "python",
	}
	if req.Name != "" {
		functionInput["name"] = req.Name
	}
	if req.Description != "" {
		functionInput["description"] = req.Description
	}
	if req.Code != "" {
		functionInput["code"] = req.Code
	}
	if len(req.Inputs) > 0 {
		functionInput["inputs"] = functionParamsToMaps(req.Inputs)
	}
	if len(req.Outputs) > 0 {
		functionInput["outputs"] = functionParamsToMaps(req.Outputs)
	}

	payload := client.UpdateToolPayload{
		Name:                req.Name,
		Description:         req.Description,
		FallbackName:        capability.Name,
		FallbackDescription: capability.Description,
		FunctionInput:       functionInput,
	}
	if err := s.Client.UpdateTool(ctx, businessDomain, capability.BoxID, capability.ToolID, payload); err != nil {
		return nil, err
	}

	return s.GetCapability(ctx, businessDomain, capability.ID)
}

func (s *Service) UpdateSkillPackage(
	ctx context.Context,
	businessDomain, capabilityID string,
	req model.RegisterSkillCapabilityRequest,
) (*model.Capability, error) {
	capability, err := s.GetCapability(ctx, businessDomain, capabilityID)
	if err != nil {
		return nil, err
	}
	if capability.Kind != "skill" || capability.SkillID == "" {
		return nil, errors.New("skill package update only supported for skill capabilities")
	}

	if err := s.Client.UpdateSkillPackage(ctx, businessDomain, capability.SkillID, client.RegisterSkillPayload{
		FileType: req.FileType,
		Filename: req.Filename,
		Content:  req.Content,
		MimeType: req.MimeType,
	}); err != nil {
		return nil, err
	}

	return s.GetCapability(ctx, businessDomain, capabilityID)
}

func (s *Service) DownloadSkillPackage(
	ctx context.Context,
	businessDomain, capabilityID string,
) ([]byte, string, error) {
	capability, err := s.GetCapability(ctx, businessDomain, capabilityID)
	if err != nil {
		return nil, "", err
	}
	if capability.Kind != "skill" || capability.SkillID == "" {
		return nil, "", errors.New("download only supported for skill capabilities")
	}

	return s.Client.DownloadSkillPackage(ctx, businessDomain, capability.SkillID)
}

func (s *Service) ListMcpTools(
	ctx context.Context,
	businessDomain, capabilityID string,
) ([]map[string]interface{}, error) {
	capability, err := s.GetCapability(ctx, businessDomain, capabilityID)
	if err != nil {
		return nil, err
	}
	if capability.Kind != "mcp" || capability.McpID == "" {
		return nil, errors.New("mcp tools only available for mcp capabilities")
	}

	return s.Client.ListMcpTools(ctx, businessDomain, capability.McpID)
}
