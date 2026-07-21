// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logic

import (
	"context"
	"sort"
	"strings"

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/capabilitieslab/client"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/capabilitieslab/model"
)

const maxAllKindWindow = 300

func (s *Service) ListCapabilities(
	ctx context.Context,
	businessDomain, kind, keyword, groupID, status string,
	page, pageSize int,
) (*model.CapabilityListResponse, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		kind = "all"
	}

	needle := strings.ToLower(strings.TrimSpace(keyword))

	switch kind {
	case "http":
		return s.listHttpCapabilitiesPaged(ctx, businessDomain, needle, groupID, status, page, pageSize)
	case "mcp":
		return s.listMcpCapabilitiesPaged(ctx, businessDomain, needle, status, page, pageSize)
	case "skill":
		return s.listSkillCapabilitiesPaged(ctx, businessDomain, needle, status, page, pageSize)
	case "function":
		return s.listFunctionCapabilitiesPaged(ctx, businessDomain, needle, groupID, status, page, pageSize)
	default:
		return s.listAllCapabilitiesPaged(ctx, businessDomain, needle, groupID, status, page, pageSize)
	}
}

func (s *Service) listAllCapabilitiesPaged(
	ctx context.Context,
	businessDomain, keyword, groupID, status string,
	page, pageSize int,
) (*model.CapabilityListResponse, error) {
	windowSize := page * pageSize
	if windowSize > maxAllKindWindow {
		windowSize = maxAllKindWindow
	}

	httpItems, _, err := s.collectHttpCapabilities(ctx, businessDomain, keyword, groupID, windowSize)
	if err != nil {
		return nil, err
	}

	mcpItems, _, err := s.collectMcpCapabilities(ctx, businessDomain, keyword, windowSize)
	if err != nil {
		return nil, err
	}

	skillItems, _, err := s.collectSkillCapabilities(ctx, businessDomain, keyword, windowSize)
	if err != nil {
		return nil, err
	}

	functionItems, _, err := s.collectFunctionCapabilities(ctx, businessDomain, keyword, groupID, windowSize)
	if err != nil {
		return nil, err
	}

	merged := mergeCapabilitiesByUpdateTime(httpItems, mcpItems, skillItems, functionItems)
	merged = filterCapabilitiesByStatus(merged, status)
	total := len(merged)

	start := (page - 1) * pageSize
	if start > len(merged) {
		start = len(merged)
	}
	end := start + pageSize
	if end > len(merged) {
		end = len(merged)
	}

	return &model.CapabilityListResponse{
		Data:     merged[start:end],
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

func (s *Service) listHttpCapabilitiesPaged(
	ctx context.Context,
	businessDomain, keyword, groupID, status string,
	page, pageSize int,
) (*model.CapabilityListResponse, error) {
	if groupID != "" {
		return s.listHttpCapabilitiesInGroupPaged(ctx, businessDomain, keyword, groupID, status, page, pageSize)
	}

	boxResp, err := s.Client.ListToolboxes(ctx, businessDomain, "", 1, 100, true)
	if err != nil {
		return nil, err
	}

	boxes := filterOpenAPIToolboxes(boxResp.Data, "")
	total, err := s.countHttpTools(ctx, businessDomain, keyword, boxes)
	if err != nil {
		return nil, err
	}

	offset := (page - 1) * pageSize
	items := make([]model.Capability, 0, pageSize)
	skipped := 0

	for _, box := range boxes {
		if len(items) >= pageSize {
			break
		}

		toolPage := 1
		for {
			resp, pageErr := s.Client.ListToolsPaged(
				ctx,
				businessDomain,
				box.BoxID,
				keyword,
				toolPage,
				pageSize,
			)
			if pageErr != nil {
				return nil, pageErr
			}

			group := groupFromBox(box)
			for _, tool := range resp.Tools {
				capability := s.httpCapabilityFromTool(box, tool, group)
				capability.Orchestration = s.orchestrationFromResourceObject(tool)
				if status != "" && status != "all" && !capabilityStatusMatches(capability.Status, status) {
					continue
				}
				if skipped < offset {
					skipped++
					continue
				}

				items = append(items, capability)
				if len(items) >= pageSize {
					break
				}
			}

			if len(items) >= pageSize || len(resp.Tools) == 0 || toolPage*pageSize >= resp.Total {
				break
			}
			toolPage++
		}
	}

	return applyStatusFilterResponse(&model.CapabilityListResponse{
		Data:     items,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, status), nil
}

func (s *Service) listHttpCapabilitiesInGroupPaged(
	ctx context.Context,
	businessDomain, keyword, groupID, status string,
	page, pageSize int,
) (*model.CapabilityListResponse, error) {
	box, err := s.findToolbox(ctx, businessDomain, groupID)
	if err != nil {
		return nil, err
	}

	resp, err := s.Client.ListToolsPaged(ctx, businessDomain, groupID, keyword, page, pageSize)
	if err != nil {
		return nil, err
	}

	boxInfo := toolboxInfoFromSnapshot(box)
	group := groupFromBox(boxInfo)

	items := make([]model.Capability, 0, len(resp.Tools))
	for _, tool := range resp.Tools {
		capability := s.httpCapabilityFromTool(boxInfo, tool, group)
		capability.Orchestration = s.orchestrationFromResourceObject(tool)
		items = append(items, capability)
	}

	return applyStatusFilterResponse(&model.CapabilityListResponse{
		Data:     items,
		Total:    resp.Total,
		Page:     page,
		PageSize: pageSize,
	}, status), nil
}

func (s *Service) collectHttpCapabilities(
	ctx context.Context,
	businessDomain, keyword, groupID string,
	limit int,
) ([]model.Capability, int, error) {
	if groupID != "" {
		return s.collectHttpCapabilitiesInGroup(ctx, businessDomain, keyword, groupID, limit)
	}

	boxResp, err := s.Client.ListToolboxes(ctx, businessDomain, "", 1, 100, true)
	if err != nil {
		return nil, 0, err
	}

	boxes := filterOpenAPIToolboxes(boxResp.Data, groupID)
	total, err := s.countHttpTools(ctx, businessDomain, keyword, boxes)
	if err != nil {
		return nil, 0, err
	}

	items := make([]model.Capability, 0)
	for _, box := range boxes {
		tools, listErr := s.listToolsForBox(ctx, businessDomain, box, keyword)
		if listErr != nil {
			return nil, 0, listErr
		}

		group := groupFromBox(box)
		for _, tool := range tools {
			capability := s.httpCapabilityFromTool(box, tool, group)
			capability.Orchestration = s.orchestrationFromResourceObject(tool)
			items = append(items, capability)
			if limit > 0 && len(items) >= limit {
				return items, total, nil
			}
		}
	}

	return items, total, nil
}

func (s *Service) collectHttpCapabilitiesInGroup(
	ctx context.Context,
	businessDomain, keyword, groupID string,
	limit int,
) ([]model.Capability, int, error) {
	box, err := s.findToolbox(ctx, businessDomain, groupID)
	if err != nil {
		return nil, 0, err
	}

	boxInfo := toolboxInfoFromSnapshot(box)

	tools, listErr := s.listToolsForBox(ctx, businessDomain, boxInfo, keyword)
	if listErr != nil {
		return nil, 0, listErr
	}

	group := groupFromBox(boxInfo)
	items := make([]model.Capability, 0, len(tools))
	for _, tool := range tools {
		capability := s.httpCapabilityFromTool(boxInfo, tool, group)
		capability.Orchestration = s.orchestrationFromResourceObject(tool)
		items = append(items, capability)
	}

	total := len(items)
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}

	return items, total, nil
}

func (s *Service) listToolsForBox(
	ctx context.Context,
	businessDomain string,
	box client.ToolboxInfo,
	keyword string,
) ([]client.ToolInfo, error) {
	if keyword == "" {
		return s.Client.ListTools(ctx, businessDomain, box.BoxID)
	}

	tools := make([]client.ToolInfo, 0)
	for page := 1; ; page++ {
		resp, err := s.Client.ListToolsPaged(ctx, businessDomain, box.BoxID, keyword, page, 100)
		if err != nil {
			return nil, err
		}
		tools = append(tools, resp.Tools...)
		if len(resp.Tools) == 0 || len(tools) >= resp.Total {
			break
		}
	}

	return tools, nil
}

func (s *Service) countHttpTools(
	ctx context.Context,
	businessDomain, keyword string,
	boxes []client.ToolboxInfo,
) (int, error) {
	if keyword == "" {
		total := 0
		for _, box := range boxes {
			total += len(box.Tools)
		}
		return total, nil
	}

	total := 0
	for _, box := range boxes {
		resp, err := s.Client.ListToolsPaged(ctx, businessDomain, box.BoxID, keyword, 1, 1)
		if err != nil {
			return 0, err
		}
		total += resp.Total
	}

	return total, nil
}

func (s *Service) httpCapabilityFromTool(
	box client.ToolboxInfo,
	tool client.ToolInfo,
	group *model.Group,
) model.Capability {
	return model.Capability{
		ID:          BuildHttpCapabilityID(box.BoxID, tool.ToolID),
		Kind:        "http",
		Name:        tool.Name,
		Description: tool.Description,
		Status:      mapToolStatus(tool.Status, box.Status),
		Group:       group,
		UpdateTime:  tool.UpdateTime,
		Audit:       auditFromTool(tool),
		ToolID:      tool.ToolID,
		BoxID:       box.BoxID,
	}
}

func filterOpenAPIToolboxes(boxes []client.ToolboxInfo, groupID string) []client.ToolboxInfo {
	filtered := make([]client.ToolboxInfo, 0, len(boxes))
	for _, box := range boxes {
		if groupID != "" && box.BoxID != groupID {
			continue
		}
		if box.MetadataType != "" && box.MetadataType != "openapi" {
			continue
		}
		filtered = append(filtered, box)
	}
	return filtered
}

func groupFromBox(box client.ToolboxInfo) *model.Group {
	return &model.Group{
		ID:         box.BoxID,
		Name:       box.BoxName,
		ServiceURL: box.BoxSvcURL,
		Status:     box.Status,
		Category:   box.BoxCategory,
	}
}

func (s *Service) listMcpCapabilitiesPaged(
	ctx context.Context,
	businessDomain, keyword, status string,
	page, pageSize int,
) (*model.CapabilityListResponse, error) {
	resp, err := s.Client.ListMcps(ctx, businessDomain, keyword, page, pageSize)
	if err != nil {
		return nil, err
	}

	return applyStatusFilterResponse(&model.CapabilityListResponse{
		Data:     mapMcpCapabilities(resp.Data),
		Total:    resp.Total,
		Page:     page,
		PageSize: pageSize,
	}, status), nil
}

func (s *Service) collectMcpCapabilities(
	ctx context.Context,
	businessDomain, keyword string,
	limit int,
) ([]model.Capability, int, error) {
	pageSize := limit
	if pageSize <= 0 {
		pageSize = 100
	}

	resp, err := s.Client.ListMcps(ctx, businessDomain, keyword, 1, pageSize)
	if err != nil {
		return nil, 0, err
	}

	items := mapMcpCapabilities(resp.Data)
	return items, resp.Total, nil
}

func (s *Service) listSkillCapabilitiesPaged(
	ctx context.Context,
	businessDomain, keyword, status string,
	page, pageSize int,
) (*model.CapabilityListResponse, error) {
	resp, err := s.Client.ListSkills(ctx, businessDomain, keyword, page, pageSize)
	if err != nil {
		return nil, err
	}

	return applyStatusFilterResponse(&model.CapabilityListResponse{
		Data:     mapSkillCapabilities(resp.Data),
		Total:    resp.Total,
		Page:     page,
		PageSize: pageSize,
	}, status), nil
}

func (s *Service) collectSkillCapabilities(
	ctx context.Context,
	businessDomain, keyword string,
	limit int,
) ([]model.Capability, int, error) {
	pageSize := limit
	if pageSize <= 0 {
		pageSize = 100
	}

	resp, err := s.Client.ListSkills(ctx, businessDomain, keyword, 1, pageSize)
	if err != nil {
		return nil, 0, err
	}

	items := mapSkillCapabilities(resp.Data)
	return items, resp.Total, nil
}

func mapMcpCapabilities(items []client.McpSummary) []model.Capability {
	capabilities := make([]model.Capability, 0, len(items))
	for _, mcp := range items {
		capabilities = append(capabilities, model.Capability{
			ID:          BuildMcpCapabilityID(mcp.McpID),
			Kind:        "mcp",
			Name:        mcp.Name,
			Description: mcp.Description,
			Status:      mapMcpSkillStatus(mcp.Status),
			UpdateTime:  mcp.UpdateTime,
			Audit:       auditFromMcp(mcp),
			McpID:       mcp.McpID,
		})
	}
	return capabilities
}

func mapSkillCapabilities(items []client.SkillSummary) []model.Capability {
	capabilities := make([]model.Capability, 0, len(items))
	for _, skill := range items {
		capabilities = append(capabilities, model.Capability{
			ID:          BuildSkillCapabilityID(skill.SkillID),
			Kind:        "skill",
			Name:        skill.Name,
			Description: skill.Description,
			Status:      mapMcpSkillStatus(skill.Status),
			UpdateTime:  skill.UpdateTime,
			Audit:       auditFromSkill(skill),
			SkillID:     skill.SkillID,
			Version:     skill.Version,
		})
	}
	return capabilities
}

func mergeCapabilitiesByUpdateTime(groups ...[]model.Capability) []model.Capability {
	total := 0
	for _, group := range groups {
		total += len(group)
	}

	merged := make([]model.Capability, 0, total)
	for _, group := range groups {
		merged = append(merged, group...)
	}

	sort.Slice(merged, func(i, j int) bool {
		return merged[i].UpdateTime > merged[j].UpdateTime
	})

	return merged
}

func filterFunctionToolboxes(boxes []client.ToolboxInfo, groupID string) []client.ToolboxInfo {
	filtered := make([]client.ToolboxInfo, 0, len(boxes))
	for _, box := range boxes {
		if groupID != "" && box.BoxID != groupID {
			continue
		}
		if box.MetadataType != "function" {
			continue
		}
		filtered = append(filtered, box)
	}
	return filtered
}

func (s *Service) listFunctionCapabilitiesPaged(
	ctx context.Context,
	businessDomain, keyword, groupID, status string,
	page, pageSize int,
) (*model.CapabilityListResponse, error) {
	if groupID != "" {
		return s.listFunctionCapabilitiesInGroupPaged(ctx, businessDomain, keyword, groupID, status, page, pageSize)
	}

	boxResp, err := s.Client.ListToolboxes(ctx, businessDomain, "", 1, 100, true)
	if err != nil {
		return nil, err
	}

	boxes := filterFunctionToolboxes(boxResp.Data, "")
	total, err := s.countHttpTools(ctx, businessDomain, keyword, boxes)
	if err != nil {
		return nil, err
	}

	offset := (page - 1) * pageSize
	items := make([]model.Capability, 0, pageSize)
	skipped := 0

	for _, box := range boxes {
		if len(items) >= pageSize {
			break
		}

		toolPage := 1
		for {
			resp, pageErr := s.Client.ListToolsPaged(
				ctx,
				businessDomain,
				box.BoxID,
				keyword,
				toolPage,
				pageSize,
			)
			if pageErr != nil {
				return nil, pageErr
			}

			group := groupFromBox(box)
			for _, tool := range resp.Tools {
				if skipped < offset {
					skipped++
					continue
				}

				items = append(items, s.functionCapabilityFromTool(box, tool, group))
				if len(items) >= pageSize {
					break
				}
			}

			if len(items) >= pageSize || len(resp.Tools) == 0 || toolPage*pageSize >= resp.Total {
				break
			}
			toolPage++
		}
	}

	return applyStatusFilterResponse(&model.CapabilityListResponse{
		Data:     items,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, status), nil
}

func (s *Service) listFunctionCapabilitiesInGroupPaged(
	ctx context.Context,
	businessDomain, keyword, groupID, status string,
	page, pageSize int,
) (*model.CapabilityListResponse, error) {
	box, err := s.findToolbox(ctx, businessDomain, groupID)
	if err != nil {
		return nil, err
	}

	resp, err := s.Client.ListToolsPaged(ctx, businessDomain, groupID, keyword, page, pageSize)
	if err != nil {
		return nil, err
	}

	boxInfo := toolboxInfoFromSnapshot(box)
	group := groupFromBox(boxInfo)

	items := make([]model.Capability, 0, len(resp.Tools))
	for _, tool := range resp.Tools {
		items = append(items, s.functionCapabilityFromTool(boxInfo, tool, group))
	}

	return applyStatusFilterResponse(&model.CapabilityListResponse{
		Data:     items,
		Total:    resp.Total,
		Page:     page,
		PageSize: pageSize,
	}, status), nil
}

func (s *Service) collectFunctionCapabilities(
	ctx context.Context,
	businessDomain, keyword, groupID string,
	limit int,
) ([]model.Capability, int, error) {
	if groupID != "" {
		return s.collectFunctionCapabilitiesInGroup(ctx, businessDomain, keyword, groupID, limit)
	}

	boxResp, err := s.Client.ListToolboxes(ctx, businessDomain, "", 1, 100, true)
	if err != nil {
		return nil, 0, err
	}

	boxes := filterFunctionToolboxes(boxResp.Data, groupID)
	total, err := s.countHttpTools(ctx, businessDomain, keyword, boxes)
	if err != nil {
		return nil, 0, err
	}

	items := make([]model.Capability, 0)
	for _, box := range boxes {
		tools, listErr := s.listToolsForBox(ctx, businessDomain, box, keyword)
		if listErr != nil {
			return nil, 0, listErr
		}

		group := groupFromBox(box)
		for _, tool := range tools {
			items = append(items, s.functionCapabilityFromTool(box, tool, group))
			if limit > 0 && len(items) >= limit {
				return items, total, nil
			}
		}
	}

	return items, total, nil
}

func (s *Service) collectFunctionCapabilitiesInGroup(
	ctx context.Context,
	businessDomain, keyword, groupID string,
	limit int,
) ([]model.Capability, int, error) {
	box, err := s.findToolbox(ctx, businessDomain, groupID)
	if err != nil {
		return nil, 0, err
	}

	boxInfo := toolboxInfoFromSnapshot(box)
	tools, err := s.listToolsForBox(ctx, businessDomain, boxInfo, keyword)
	if err != nil {
		return nil, 0, err
	}

	group := groupFromBox(boxInfo)

	items := make([]model.Capability, 0, len(tools))
	for _, tool := range tools {
		items = append(items, s.functionCapabilityFromTool(boxInfo, tool, group))
		if limit > 0 && len(items) >= limit {
			break
		}
	}

	return items, len(items), nil
}

func mapMcpSkillStatus(status string) string {
	switch status {
	case "published":
		return "published"
	case "offline":
		return "offline"
	default:
		return "draft"
	}
}
