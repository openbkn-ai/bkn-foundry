// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logic

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/capabilitieslab/client"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/capabilitieslab/model"
)

func (s *Service) ListCatalog(
	ctx context.Context,
	kind, keyword string,
	page, pageSize int,
) (*model.CatalogListResponse, error) {
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

	installed, err := s.collectInstalledCatalogIDs(ctx)
	if err != nil {
		return nil, err
	}

	switch kind {
	case "http":
		return s.listHttpCatalogPaged(ctx, keyword, page, pageSize, installed.boxes)
	case "mcp":
		return s.listMcpCatalogPaged(ctx, keyword, page, pageSize, installed.mcps)
	case "skill":
		return s.listSkillCatalogPaged(ctx, keyword, page, pageSize, installed.skills)
	default:
		return s.listAllCatalogPaged(ctx, keyword, page, pageSize, installed)
	}
}

type installedCatalogIDs struct {
	boxes  map[string]struct{}
	mcps   map[string]struct{}
	skills map[string]struct{}
}

const maxInstalledLookup = 100

func (s *Service) collectInstalledCatalogIDs(
	ctx context.Context,
) (*installedCatalogIDs, error) {
	result := &installedCatalogIDs{
		boxes:  map[string]struct{}{},
		mcps:   map[string]struct{}{},
		skills: map[string]struct{}{},
	}

	httpItems, _, err := s.collectHttpCapabilities(ctx, "", "", maxInstalledLookup)
	if err != nil {
		return nil, err
	}
	for _, item := range httpItems {
		if item.BoxID != "" {
			result.boxes[item.BoxID] = struct{}{}
		}
	}

	mcpItems, _, err := s.collectMcpCapabilities(ctx, "", maxInstalledLookup)
	if err != nil {
		return nil, err
	}
	for _, item := range mcpItems {
		if item.McpID != "" {
			result.mcps[item.McpID] = struct{}{}
		}
	}

	skillItems, _, err := s.collectSkillCapabilities(ctx, "", maxInstalledLookup)
	if err != nil {
		return nil, err
	}
	for _, item := range skillItems {
		if item.SkillID != "" {
			result.skills[item.SkillID] = struct{}{}
		}
	}

	return result, nil
}

func (s *Service) listAllCatalogPaged(
	ctx context.Context,
	keyword string,
	page, pageSize int,
	installed *installedCatalogIDs,
) (*model.CatalogListResponse, error) {
	windowSize := page * pageSize
	if windowSize > maxAllKindWindow {
		windowSize = maxAllKindWindow
	}

	httpItems, httpTotal, err := s.collectHttpCatalog(ctx, keyword, windowSize, installed.boxes)
	if err != nil {
		return nil, err
	}
	mcpItems, mcpTotal, err := s.collectMcpCatalog(ctx, keyword, windowSize, installed.mcps)
	if err != nil {
		return nil, err
	}
	skillItems, skillTotal, err := s.collectSkillCatalog(ctx, keyword, windowSize, installed.skills)
	if err != nil {
		return nil, err
	}

	merged := mergeCatalogByUpdateTime(httpItems, mcpItems, skillItems)
	total := httpTotal + mcpTotal + skillTotal

	start := (page - 1) * pageSize
	if start > len(merged) {
		start = len(merged)
	}
	end := start + pageSize
	if end > len(merged) {
		end = len(merged)
	}

	return &model.CatalogListResponse{
		Data:     merged[start:end],
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

func (s *Service) listHttpCatalogPaged(
	ctx context.Context,
	keyword string,
	page, pageSize int,
	installed map[string]struct{},
) (*model.CatalogListResponse, error) {
	resp, err := s.Client.ListToolboxMarket(ctx, keyword, page, pageSize)
	if err != nil {
		return nil, err
	}

	items := make([]model.CatalogEntry, 0, len(resp.Data))
	for _, item := range resp.Data {
		_, isInstalled := installed[item.BoxID]
		items = append(items, model.CatalogEntry{
			ID:          item.BoxID,
			Kind:        "http",
			Name:        item.BoxName,
			Description: item.BoxDesc,
			Status:      item.Status,
			UpdateTime:  item.UpdateTime,
			Installed:   isInstalled,
		})
	}

	return &model.CatalogListResponse{
		Data:     items,
		Total:    resp.Total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

func (s *Service) listMcpCatalogPaged(
	ctx context.Context,
	keyword string,
	page, pageSize int,
	installed map[string]struct{},
) (*model.CatalogListResponse, error) {
	resp, err := s.Client.ListMcpMarket(ctx, keyword, page, pageSize)
	if err != nil {
		return nil, err
	}

	items := make([]model.CatalogEntry, 0, len(resp.Data))
	for _, item := range resp.Data {
		_, isInstalled := installed[item.McpID]
		items = append(items, model.CatalogEntry{
			ID:          item.McpID,
			Kind:        "mcp",
			Name:        item.Name,
			Description: item.Description,
			Status:      item.Status,
			UpdateTime:  item.UpdateTime,
			Installed:   isInstalled,
		})
	}

	return &model.CatalogListResponse{
		Data:     items,
		Total:    resp.Total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

func (s *Service) listSkillCatalogPaged(
	ctx context.Context,
	keyword string,
	page, pageSize int,
	installed map[string]struct{},
) (*model.CatalogListResponse, error) {
	resp, err := s.Client.ListSkillMarket(ctx, keyword, page, pageSize)
	if err != nil {
		return nil, err
	}

	items := make([]model.CatalogEntry, 0, len(resp.Data))
	for _, item := range resp.Data {
		_, isInstalled := installed[item.SkillID]
		items = append(items, model.CatalogEntry{
			ID:          item.SkillID,
			Kind:        "skill",
			Name:        item.Name,
			Description: item.Description,
			Status:      item.Status,
			UpdateTime:  item.UpdateTime,
			Installed:   isInstalled,
			Version:     item.Version,
		})
	}

	return &model.CatalogListResponse{
		Data:     items,
		Total:    resp.Total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

func (s *Service) collectHttpCatalog(
	ctx context.Context,
	keyword string,
	limit int,
	installed map[string]struct{},
) ([]model.CatalogEntry, int, error) {
	pageSize := limit
	if pageSize <= 0 {
		pageSize = 100
	}
	resp, err := s.Client.ListToolboxMarket(ctx, keyword, 1, pageSize)
	if err != nil {
		return nil, 0, err
	}

	items := make([]model.CatalogEntry, 0, len(resp.Data))
	for _, item := range resp.Data {
		_, isInstalled := installed[item.BoxID]
		items = append(items, model.CatalogEntry{
			ID:          item.BoxID,
			Kind:        "http",
			Name:        item.BoxName,
			Description: item.BoxDesc,
			Status:      item.Status,
			UpdateTime:  item.UpdateTime,
			Installed:   isInstalled,
		})
	}
	return items, resp.Total, nil
}

func (s *Service) collectMcpCatalog(
	ctx context.Context,
	keyword string,
	limit int,
	installed map[string]struct{},
) ([]model.CatalogEntry, int, error) {
	pageSize := limit
	if pageSize <= 0 {
		pageSize = 100
	}
	resp, err := s.Client.ListMcpMarket(ctx, keyword, 1, pageSize)
	if err != nil {
		return nil, 0, err
	}

	items := make([]model.CatalogEntry, 0, len(resp.Data))
	for _, item := range resp.Data {
		_, isInstalled := installed[item.McpID]
		items = append(items, model.CatalogEntry{
			ID:          item.McpID,
			Kind:        "mcp",
			Name:        item.Name,
			Description: item.Description,
			Status:      item.Status,
			UpdateTime:  item.UpdateTime,
			Installed:   isInstalled,
		})
	}
	return items, resp.Total, nil
}

func (s *Service) collectSkillCatalog(
	ctx context.Context,
	keyword string,
	limit int,
	installed map[string]struct{},
) ([]model.CatalogEntry, int, error) {
	pageSize := limit
	if pageSize <= 0 {
		pageSize = 100
	}
	resp, err := s.Client.ListSkillMarket(ctx, keyword, 1, pageSize)
	if err != nil {
		return nil, 0, err
	}

	items := make([]model.CatalogEntry, 0, len(resp.Data))
	for _, item := range resp.Data {
		_, isInstalled := installed[item.SkillID]
		items = append(items, model.CatalogEntry{
			ID:          item.SkillID,
			Kind:        "skill",
			Name:        item.Name,
			Description: item.Description,
			Status:      item.Status,
			UpdateTime:  item.UpdateTime,
			Installed:   isInstalled,
			Version:     item.Version,
		})
	}
	return items, resp.Total, nil
}

func mergeCatalogByUpdateTime(chunks ...[]model.CatalogEntry) []model.CatalogEntry {
	merged := make([]model.CatalogEntry, 0)
	for _, chunk := range chunks {
		merged = append(merged, chunk...)
	}
	sort.SliceStable(merged, func(i, j int) bool {
		return merged[i].UpdateTime > merged[j].UpdateTime
	})
	return merged
}

func (s *Service) InstallFromCatalog(
	ctx context.Context,
	req model.InstallCatalogRequest,
) (*model.InstallCatalogResponse, error) {
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	sourceID := strings.TrimSpace(req.SourceID)
	if sourceID == "" {
		return nil, errors.New("source_id is required")
	}

	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "create"
	}

	switch kind {
	case "http":
		return s.installToolboxFromCatalog(ctx, sourceID, mode, req.Name)
	case "mcp":
		return s.installMcpFromCatalog(ctx, sourceID, mode, req.Name)
	case "skill":
		return s.installSkillFromCatalog(ctx, sourceID)
	default:
		return nil, fmt.Errorf("unsupported catalog kind %q", kind)
	}
}

func (s *Service) installToolboxFromCatalog(
	ctx context.Context,
	sourceID, mode, name string,
) (*model.InstallCatalogResponse, error) {
	exported, err := s.Client.ExportImpex(ctx, s.DefaultUserID, "toolbox", sourceID)
	if err != nil {
		return nil, err
	}

	cloneName := strings.TrimSpace(name)
	if cloneName == "" {
		cloneName = impexNameFromExport("toolbox", exported)
	}
	if mode == "create" {
		cloneName = uniqueCatalogInstallName(cloneName)
	}

	importData := exported
	targetBoxID := sourceID
	if mode == "create" {
		cloned, newBoxID, cloneErr := cloneImpexForCreate("toolbox", exported, cloneName)
		if cloneErr != nil {
			return nil, cloneErr
		}
		importData = cloned
		targetBoxID = newBoxID
	}

	if err := s.Client.ImportImpex(ctx, s.DefaultUserID, "toolbox", mode, importData); err != nil {
		return nil, err
	}

	capabilities, err := s.capabilitiesForToolbox(ctx, targetBoxID)
	if err != nil {
		return nil, err
	}

	return &model.InstallCatalogResponse{
		ComponentType: "toolbox",
		Mode:          mode,
		Capabilities:  capabilities,
	}, nil
}

func (s *Service) installMcpFromCatalog(
	ctx context.Context,
	sourceID, mode, name string,
) (*model.InstallCatalogResponse, error) {
	exported, err := s.Client.ExportImpex(ctx, s.DefaultUserID, "mcp", sourceID)
	if err != nil {
		return nil, err
	}

	cloneName := strings.TrimSpace(name)
	if cloneName == "" {
		cloneName = impexNameFromExport("mcp", exported)
	}
	if mode == "create" {
		cloneName = uniqueCatalogInstallName(cloneName)
	}

	importData := exported
	targetMcpID := sourceID
	if mode == "create" {
		cloned, newMcpID, cloneErr := cloneImpexForCreate("mcp", exported, cloneName)
		if cloneErr != nil {
			return nil, cloneErr
		}
		importData = cloned
		targetMcpID = newMcpID
	}

	if err := s.Client.ImportImpex(ctx, s.DefaultUserID, "mcp", mode, importData); err != nil {
		return nil, err
	}

	capability, err := s.GetCapability(ctx, BuildMcpCapabilityID(targetMcpID))
	if err != nil {
		return nil, err
	}

	return &model.InstallCatalogResponse{
		ComponentType: "mcp",
		Mode:          mode,
		Capabilities:  []model.Capability{*capability},
	}, nil
}

func (s *Service) installSkillFromCatalog(
	ctx context.Context,
	sourceID string,
) (*model.InstallCatalogResponse, error) {
	content, filename, err := s.Client.DownloadSkillMarketPackage(ctx, sourceID, s.DefaultUserID)
	if err != nil {
		return nil, err
	}

	capability, err := s.RegisterSkillCapability(ctx, model.RegisterSkillCapabilityRequest{
		FileType: "zip",
		Category: "other_category",
		Source:   "market",
		Filename: filename,
		Content:  content,
		MimeType: "application/zip",
	})
	if err != nil {
		return nil, err
	}

	return &model.InstallCatalogResponse{
		ComponentType: "skill",
		Mode:          "create",
		Capabilities:  []model.Capability{*capability},
	}, nil
}

func (s *Service) capabilitiesForToolbox(
	ctx context.Context,
	boxID string,
) ([]model.Capability, error) {
	box, err := s.findToolbox(ctx, boxID)
	if err != nil {
		return nil, err
	}

	tools, err := s.listToolsForBox(ctx, client.ToolboxInfo{
		BoxID:     box.BoxID,
		BoxName:   box.BoxName,
		BoxSvcURL: box.BoxSvcURL,
		Status:    box.Status,
	}, "")
	if err != nil {
		return nil, err
	}

	group := &model.Group{
		ID:         box.BoxID,
		Name:       box.BoxName,
		ServiceURL: box.BoxSvcURL,
		Status:     box.Status,
	}

	capabilities := make([]model.Capability, 0, len(tools))
	for _, tool := range tools {
		capabilities = append(capabilities, s.httpCapabilityFromTool(client.ToolboxInfo{
			BoxID:     box.BoxID,
			BoxName:   box.BoxName,
			BoxSvcURL: box.BoxSvcURL,
			Status:    box.Status,
		}, tool, group))
	}

	return capabilities, nil
}

func uniqueCatalogInstallName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "catalog_install_" + newUUID()[:8]
	}
	if strings.HasSuffix(name, "_copy") {
		return name + "_" + newUUID()[:8]
	}
	return name + "_copy"
}
