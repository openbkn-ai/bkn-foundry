package logic

import (
	"context"
	"errors"
	"fmt"

	"github.com/openbkn-ai/adp/execution-factory/capabilities-lab/server/client"
	"github.com/openbkn-ai/adp/execution-factory/capabilities-lab/server/model"
)

func (s *Service) UpdateHttpCapability(
	ctx context.Context,
	businessDomain, capabilityID string,
	req model.UpdateHttpCapabilityRequest,
) (*model.Capability, error) {
	capability, err := s.GetCapability(ctx, businessDomain, capabilityID)
	if err != nil {
		return nil, err
	}
	if capability.Kind != "http" {
		return nil, errors.New("update only supported for http capabilities")
	}
	if capability.BoxID == "" || capability.ToolID == "" {
		return nil, errors.New("missing tool reference")
	}

	openapiSpec := req.OpenAPISpec
	if req.Name != "" && openapiSpec != "" {
		openapiSpec = applyCapabilityName(openapiSpec, req.Name)
	}

	payload := client.UpdateToolPayload{
		Name:                req.Name,
		Description:         req.Description,
		FallbackName:        capability.Name,
		FallbackDescription: capability.Description,
		OpenAPISpec:         openapiSpec,
	}
	if err := s.Client.UpdateTool(ctx, businessDomain, capability.BoxID, capability.ToolID, payload); err != nil {
		return nil, err
	}

	return s.GetCapability(ctx, businessDomain, capabilityID)
}

func (s *Service) DeleteCapability(ctx context.Context, businessDomain, capabilityID string) error {
	capability, err := s.GetCapability(ctx, businessDomain, capabilityID)
	if err != nil {
		return err
	}

	switch capability.Kind {
	case "http", "function":
		if capability.BoxID == "" || capability.ToolID == "" {
			return errors.New("missing tool reference")
		}
		return s.Client.DeleteTools(ctx, businessDomain, capability.BoxID, []string{capability.ToolID})
	case "mcp":
		if capability.McpID == "" {
			return errors.New("missing mcp id")
		}
		return s.Client.DeleteMcp(ctx, businessDomain, capability.McpID)
	case "skill":
		if capability.SkillID == "" {
			return errors.New("missing skill id")
		}
		return s.Client.DeleteSkill(ctx, businessDomain, capability.SkillID)
	default:
		return fmt.Errorf("unsupported capability kind")
	}
}

func (s *Service) RegisterMcpCapability(
	ctx context.Context,
	businessDomain string,
	req model.RegisterMcpCapabilityRequest,
) (*model.Capability, error) {
	mcpID, err := s.Client.RegisterMcp(ctx, businessDomain, client.RegisterMcpPayload{
		Name:         req.Name,
		Description:  req.Description,
		Mode:         req.Mode,
		URL:          req.URL,
		Headers:      req.Headers,
		Category:     req.Category,
		CreationType: req.CreationType,
	})
	if err != nil {
		return nil, err
	}

	return s.GetCapability(ctx, businessDomain, BuildMcpCapabilityID(mcpID))
}

func (s *Service) RegisterSkillCapability(
	ctx context.Context,
	businessDomain string,
	req model.RegisterSkillCapabilityRequest,
) (*model.Capability, error) {
	skill, err := s.Client.RegisterSkill(ctx, businessDomain, client.RegisterSkillPayload{
		FileType: req.FileType,
		Category: req.Category,
		Source:   req.Source,
		Filename: req.Filename,
		Content:  req.Content,
		MimeType: req.MimeType,
	})
	if err != nil {
		return nil, err
	}

	return s.GetCapability(ctx, businessDomain, BuildSkillCapabilityID(skill.SkillID))
}
