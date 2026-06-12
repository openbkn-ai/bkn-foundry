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

func (s *Service) CreateFunctionCapability(
	ctx context.Context,
	businessDomain string,
	req model.CreateFunctionCapabilityRequest,
) (*model.CreateFunctionCapabilityResponse, error) {
	if strings.TrimSpace(req.Code) == "" {
		return nil, errors.New("code is required")
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "function_capability"
	}

	category := req.Category
	if category == "" {
		category = "other_category"
	}

	serviceURL := strings.TrimSpace(req.ServiceURL)
	if serviceURL == "" {
		serviceURL = strings.TrimRight(s.Client.BaseURL, "/") + "/api/agent-operator-integration/v1"
	}

	groupName, boxID, err := s.resolveFunctionGroup(ctx, businessDomain, req.Group, name)
	if err != nil {
		return nil, err
	}

	if boxID == "" {
		created, createErr := s.Client.CreateToolbox(
			ctx,
			businessDomain,
			client.CreateFunctionToolboxPayload(groupName, req.Description, serviceURL, category),
		)
		if createErr != nil {
			return nil, createErr
		}
		boxID = created.BoxID
	}

	inputs := functionParamsToMaps(req.Inputs)
	outputs := functionParamsToMaps(req.Outputs)
	if len(inputs) == 0 {
		inputs = []map[string]interface{}{{"name": "event", "type": "object"}}
	}
	if len(outputs) == 0 {
		outputs = []map[string]interface{}{{"name": "result", "type": "object"}}
	}

	toolResp, toolErr := s.Client.CreateFunctionTool(ctx, businessDomain, boxID, client.FunctionToolPayload{
		Name:        name,
		Description: req.Description,
		Code:        req.Code,
		ScriptType:  "python",
		Inputs:      inputs,
		Outputs:     outputs,
	})
	if toolErr != nil {
		return nil, toolErr
	}
	if toolResp.FailureCount > 0 || len(toolResp.SuccessIDs) == 0 {
		if len(toolResp.Failures) > 0 && toolResp.Failures[0].Message() != "" {
			return nil, errors.New(toolResp.Failures[0].Message())
		}
		return nil, errors.New("function tool creation failed")
	}

	capability, capErr := s.GetCapability(
		ctx,
		businessDomain,
		BuildFunctionCapabilityID(boxID, toolResp.SuccessIDs[0]),
	)
	if capErr != nil {
		return nil, capErr
	}

	return &model.CreateFunctionCapabilityResponse{Capability: *capability}, nil
}

func (s *Service) resolveFunctionGroup(
	ctx context.Context,
	businessDomain string,
	group model.GroupInput,
	defaultName string,
) (groupName, boxID string, err error) {
	createReq := model.CreateHttpCapabilityRequest{Group: group}
	groupName, boxID, err = s.resolveGroup(ctx, businessDomain, createReq)
	if err != nil {
		return "", "", err
	}
	if groupName == "" && boxID == "" {
		groupName = defaultName + "_group"
	}
	return groupName, boxID, nil
}

func functionParamsToMaps(params []model.FunctionParameterDef) []map[string]interface{} {
	items := make([]map[string]interface{}, 0, len(params))
	for _, param := range params {
		name := strings.TrimSpace(param.Name)
		if name == "" {
			continue
		}
		item := map[string]interface{}{
			"name": name,
			"type": param.Type,
		}
		if param.Type == "" {
			item["type"] = "string"
		}
		if param.Description != "" {
			item["description"] = param.Description
		}
		items = append(items, item)
	}
	return items
}

func (s *Service) ExecutePython(
	ctx context.Context,
	businessDomain string,
	req model.ExecutePythonRequest,
) (*model.ExecutePythonResponse, error) {
	if strings.TrimSpace(req.Code) == "" {
		return nil, errors.New("code is required")
	}

	resp, err := s.Client.ExecuteFunction(ctx, businessDomain, s.DefaultUserID, client.ExecuteFunctionRequest{
		Code:     req.Code,
		Event:    req.Event,
		Language: "python",
		Timeout:  req.Timeout,
	})
	if err != nil {
		return nil, err
	}

	output := decodeFunctionOutput(resp)
	return &model.ExecutePythonResponse{
		Output:     output,
		Stdout:     resp.Stdout,
		Stderr:     resp.Stderr,
		Error:      resp.Error,
		DurationMs: resp.DurationMs,
	}, nil
}

func decodeFunctionOutput(resp *client.ExecuteFunctionResponse) interface{} {
	if len(resp.Result) > 0 && json.Valid(resp.Result) {
		var parsed map[string]interface{}
		if err := json.Unmarshal(resp.Result, &parsed); err == nil {
			if inner, ok := parsed["result"]; ok {
				return inner
			}
			var generic interface{}
			if err := json.Unmarshal(resp.Result, &generic); err == nil {
				return generic
			}
		}
	}
	if len(resp.Data) > 0 && json.Valid(resp.Data) {
		var parsed interface{}
		if err := json.Unmarshal(resp.Data, &parsed); err == nil {
			return parsed
		}
	}
	return nil
}

func (s *Service) GetPythonTemplate(ctx context.Context, businessDomain string) (string, error) {
	return s.Client.GetPythonTemplate(ctx, businessDomain)
}

func (s *Service) ParseMcpSse(
	ctx context.Context,
	businessDomain string,
	req model.ParseMcpSseRequest,
) (*model.ParseMcpSseResponse, error) {
	if strings.TrimSpace(req.URL) == "" {
		return nil, errors.New("url is required")
	}

	resp, err := s.Client.ParseMcpSse(ctx, businessDomain, client.McpParseSseRequest{
		URL:     req.URL,
		Mode:    req.Mode,
		Headers: req.Headers,
	})
	if err != nil {
		return nil, err
	}

	tools := make([]model.McpParsedTool, 0, len(resp.Tools))
	for _, tool := range resp.Tools {
		tools = append(tools, model.McpParsedTool{
			Name:        tool.Name,
			Description: tool.Description,
		})
	}

	return &model.ParseMcpSseResponse{Tools: tools}, nil
}

func (s *Service) GetSkillContent(
	ctx context.Context,
	businessDomain, capabilityID string,
) (*model.SkillContentResponse, error) {
	skillID, ok := ParseSkillCapabilityID(capabilityID)
	if !ok {
		return nil, fmt.Errorf("invalid skill capability id")
	}

	content, err := s.Client.GetSkillManagementContent(ctx, businessDomain, skillID)
	if err != nil {
		return nil, err
	}

	files := make([]model.SkillFileSummary, 0, len(content.Files))
	for _, file := range content.Files {
		files = append(files, model.SkillFileSummary{
			RelPath:  file.RelPath,
			FileType: file.FileType,
			MimeType: file.MimeType,
			Size:     file.Size,
		})
	}

	return &model.SkillContentResponse{
		Content:     content.Content,
		FileType:    content.FileType,
		Files:       files,
		DownloadURL: content.URL,
	}, nil
}

func (s *Service) ReadSkillFile(
	ctx context.Context,
	businessDomain, capabilityID string,
	req model.ReadSkillFileRequest,
) (*model.ReadSkillFileResponse, error) {
	skillID, ok := ParseSkillCapabilityID(capabilityID)
	if !ok {
		return nil, fmt.Errorf("invalid skill capability id")
	}
	if strings.TrimSpace(req.RelPath) == "" {
		return nil, errors.New("rel_path is required")
	}

	mode := req.ResponseMode
	if mode == "" {
		mode = "content"
	}

	resp, err := s.Client.ReadSkillManagementFile(ctx, businessDomain, skillID, req.RelPath, mode)
	if err != nil {
		return nil, err
	}

	return &model.ReadSkillFileResponse{
		RelPath:  resp.RelPath,
		URL:      resp.URL,
		Content:  resp.Content,
		MimeType: resp.MimeType,
		FileType: resp.FileType,
		Size:     resp.Size,
	}, nil
}

func (s *Service) functionCapabilityFromTool(
	box client.ToolboxInfo,
	tool client.ToolInfo,
	group *model.Group,
) model.Capability {
	return model.Capability{
		ID:          BuildFunctionCapabilityID(box.BoxID, tool.ToolID),
		Kind:        "function",
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
