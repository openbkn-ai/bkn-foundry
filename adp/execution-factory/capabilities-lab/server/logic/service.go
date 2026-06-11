package logic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/openbkn-ai/adp/execution-factory/capabilities-lab/server/client"
	"github.com/openbkn-ai/adp/execution-factory/capabilities-lab/server/model"
)

type Service struct {
	Client        *client.OperatorIntegrationClient
	DefaultUserID string
}

type toolboxSnapshot struct {
	BoxID       string
	BoxName     string
	BoxSvcURL   string
	BoxCategory string
	CreateUser  string
	CreateTime  int64
	UpdateUser  string
	Status      string
	UpdateTime  int64
	ReleaseUser string
	ReleaseTime int64
}

func toolboxInfoFromSnapshot(box *toolboxSnapshot) client.ToolboxInfo {
	if box == nil {
		return client.ToolboxInfo{}
	}
	return client.ToolboxInfo{
		BoxID:       box.BoxID,
		BoxName:     box.BoxName,
		BoxSvcURL:   box.BoxSvcURL,
		BoxCategory: box.BoxCategory,
		CreateUser:  box.CreateUser,
		CreateTime:  box.CreateTime,
		UpdateUser:  box.UpdateUser,
		Status:      box.Status,
		UpdateTime:  box.UpdateTime,
		ReleaseUser: box.ReleaseUser,
		ReleaseTime: box.ReleaseTime,
	}
}

func (s *Service) ListGroups(
	ctx context.Context,
	businessDomain, keyword string,
	page, pageSize int,
) (*model.GroupListResponse, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}

	resp, err := s.Client.ListToolboxes(ctx, businessDomain, keyword, page, pageSize, false)
	if err != nil {
		return nil, err
	}

	groups := make([]model.Group, 0, len(resp.Data))
	for _, box := range resp.Data {
		if box.MetadataType != "" && box.MetadataType != "openapi" {
			continue
		}

		groups = append(groups, model.Group{
			ID:           box.BoxID,
			Name:         box.BoxName,
			ServiceURL:   box.BoxSvcURL,
			Status:       box.Status,
			Category:     box.BoxCategory,
			ToolCount:    len(box.Tools),
			UpdateTime:   box.UpdateTime,
			MetadataType: box.MetadataType,
		})
	}

	return &model.GroupListResponse{
		Data:     groups,
		Total:    resp.Total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

func (s *Service) CreateHttpCapability(
	ctx context.Context,
	businessDomain string,
	req model.CreateHttpCapabilityRequest,
) (*model.CreateHttpCapabilityResponse, error) {
	if req.Name != "" {
		req.OpenAPISpec = applyCapabilityName(req.OpenAPISpec, req.Name)
	}

	groupName, boxID, err := s.resolveGroup(ctx, businessDomain, req)
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
		if bundle.FailureCount > 0 || len(bundle.ToolIDs) == 0 {
			if len(bundle.Failures) > 0 {
				return nil, errors.New(bundle.Failures[0])
			}
			return nil, errors.New("bundle registration failed")
		}

		toolID := bundle.ToolIDs[0]
		boxID = bundle.BoxID
		capability, capErr := s.GetCapability(ctx, businessDomain, BuildHttpCapabilityID(boxID, toolID))
		if capErr != nil {
			return nil, capErr
		}

		if len(bundle.Links) > 0 {
			capability.Orchestration = &model.Orchestration{
				Enabled:    true,
				OperatorID: bundle.Links[0].OperatorID,
			}
		}

		response := &model.CreateHttpCapabilityResponse{Capability: *capability}
		for _, link := range bundle.Links {
			response.Links = append(response.Links, model.Link{
				OperatorID: link.OperatorID,
				ToolID:     link.ToolID,
			})
		}

		return response, nil
	}

	if boxID == "" {
		created, createErr := s.Client.CreateToolbox(ctx, businessDomain, client.CreateToolboxPayload(
			groupName,
			req.Description,
			req.ServiceURL,
			category,
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
	if toolResp.FailureCount > 0 || len(toolResp.SuccessIDs) == 0 {
		if len(toolResp.Failures) > 0 && toolResp.Failures[0].Error != "" {
			return nil, errors.New(toolResp.Failures[0].Error)
		}
		return nil, errors.New("tool creation failed")
	}

	capability, capErr := s.GetCapability(ctx, businessDomain, BuildHttpCapabilityID(boxID, toolResp.SuccessIDs[0]))
	if capErr != nil {
		return nil, capErr
	}

	return &model.CreateHttpCapabilityResponse{Capability: *capability}, nil
}

func (s *Service) findToolbox(
	ctx context.Context,
	businessDomain, boxID string,
) (*toolboxSnapshot, error) {
	boxResp, err := s.Client.ListToolboxes(ctx, businessDomain, "", 1, 100, true)
	if err != nil {
		return nil, err
	}

	for _, candidate := range boxResp.Data {
		if candidate.BoxID == boxID {
			return &toolboxSnapshot{
				BoxID:       candidate.BoxID,
				BoxName:     candidate.BoxName,
				BoxSvcURL:   candidate.BoxSvcURL,
				BoxCategory: candidate.BoxCategory,
				CreateUser:  candidate.CreateUser,
				CreateTime:  candidate.CreateTime,
				UpdateUser:  candidate.UpdateUser,
				Status:      candidate.Status,
				UpdateTime:  candidate.UpdateTime,
				ReleaseUser: candidate.ReleaseUser,
				ReleaseTime: candidate.ReleaseTime,
			}, nil
		}
	}

	return nil, fmt.Errorf("group not found")
}

func (s *Service) resolveGroup(
	ctx context.Context,
	businessDomain string,
	req model.CreateHttpCapabilityRequest,
) (groupName, boxID string, err error) {
	mode := strings.ToLower(strings.TrimSpace(req.Group.Mode))
	if mode == "" {
		mode = "auto"
	}

	switch mode {
	case "existing":
		boxID = strings.TrimSpace(req.Group.BoxID)
		if boxID == "" {
			return "", "", fmt.Errorf("group.box_id is required for existing mode")
		}
		return "", boxID, nil
	case "new":
		groupName = strings.TrimSpace(req.Group.Name)
		if groupName == "" {
			return "", "", fmt.Errorf("group.name is required for new mode")
		}
		return groupName, "", nil
	default:
		groupName = DeriveAutoGroupName(req.ServiceURL)
		resp, listErr := s.Client.ListToolboxes(ctx, businessDomain, groupName, 1, 5, false)
		if listErr != nil {
			return "", "", listErr
		}
		for _, box := range resp.Data {
			if strings.EqualFold(box.BoxName, groupName) {
				return groupName, box.BoxID, nil
			}
		}
		return groupName, "", nil
	}
}

func mapToolStatus(toolStatus, boxStatus string) string {
	if boxStatus == "offline" {
		return "offline"
	}
	if boxStatus == "published" {
		return "published"
	}
	if toolStatus == "enabled" {
		return "published"
	}
	return "draft"
}

func extractEndpoint(openapiJSON string) *model.Endpoint {
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal([]byte(openapiJSON), &doc); err != nil {
		return nil
	}

	for path, methods := range doc.Paths {
		for method := range methods {
			return &model.Endpoint{
				Method: strings.ToUpper(method),
				Path:   path,
			}
		}
	}

	return nil
}
