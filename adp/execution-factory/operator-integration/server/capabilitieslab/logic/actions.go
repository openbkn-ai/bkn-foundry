// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logic

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/capabilitieslab/client"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/capabilitieslab/model"
)

func (s *Service) ImportOpenApiCapabilities(
	ctx context.Context,
	req model.ImportOpenApiCapabilityRequest,
) (*model.ImportOpenApiCapabilityResponse, error) {
	createReq := model.CreateHttpCapabilityRequest{
		OpenAPISpec: req.OpenAPISpec,
		ServiceURL:  req.ServiceURL,
		Description: req.Description,
		Category:    req.Category,
		Group:       req.Group,
	}
	if createReq.Group.Mode == "" {
		createReq.Group.Mode = "auto"
	}

	groupName, boxID, err := s.resolveGroup(ctx, createReq)
	if err != nil {
		return nil, err
	}

	category := req.Category
	if category == "" {
		category = "other_category"
	}

	if boxID == "" {
		created, createErr := s.Client.CreateToolbox(ctx, client.CreateToolboxPayload(
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

	toolResp, toolErr := s.Client.CreateTool(ctx, boxID, toolPayload)
	if toolErr != nil {
		return nil, toolErr
	}

	response := &model.ImportOpenApiCapabilityResponse{BoxID: boxID}
	for _, toolID := range toolResp.SuccessIDs {
		capability, capErr := s.GetCapability(ctx, BuildHttpCapabilityID(boxID, toolID))
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
	capabilityID string,
	req model.DebugCapabilityRequest,
) (*model.DebugCapabilityResponse, error) {
	kind := ParseCapabilityKind(capabilityID)

	switch kind {
	case "http", "function":
		boxID, toolID, ok := parseToolCapabilityID(capabilityID)
		if !ok {
			return nil, errors.New("invalid capability id")
		}

		resp, err := s.Client.DebugTool(ctx, boxID, toolID, client.DebugToolRequest{
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

		resp, err := s.Client.DebugMcpTool(ctx, mcpID, req.ToolName, req.Body)
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
	capabilityID string,
) (*model.VersionListResponse, error) {
	capability, err := s.GetCapability(ctx, capabilityID)
	if err != nil {
		return nil, err
	}

	switch capability.Kind {
	case "skill":
		history, histErr := s.Client.GetSkillHistory(ctx, capability.SkillID)
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
	capabilityID string,
	req model.RepublishVersionRequest,
) error {
	capability, err := s.GetCapability(ctx, capabilityID)
	if err != nil {
		return err
	}

	mode := strings.ToLower(req.Mode)
	switch capability.Kind {
	case "skill":
		if mode == "publish" {
			return s.Client.PublishSkillHistory(ctx, capability.SkillID, req.Version)
		}
		return s.Client.RepublishSkillHistory(ctx, capability.SkillID, req.Version)
	default:
		return errors.New("republish not supported for this capability kind")
	}
}

func (s *Service) PublishCapability(
	ctx context.Context,
	capabilityID string,
	status string,
) error {
	capability, err := s.GetCapability(ctx, capabilityID)
	if err != nil {
		return err
	}

	switch capability.Kind {
	case "http", "function":
		if capability.BoxID == "" {
			return errors.New("missing group for capability")
		}
		return s.Client.UpdateToolboxStatus(ctx, capability.BoxID, status)
	case "skill":
		return s.Client.UpdateSkillStatus(ctx, capability.SkillID, mapSkillPublishStatus(status))
	case "mcp":
		return s.Client.UpdateMcpStatus(ctx, capability.McpID, mapMcpPublishStatus(status))
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

func (s *Service) PublishGroup(ctx context.Context, groupID, status string) error {
	return s.Client.UpdateToolboxStatus(ctx, groupID, status)
}
