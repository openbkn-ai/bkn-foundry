package logic

import (
	"context"
	"strings"

	"github.com/openbkn-ai/adp/execution-factory/capabilities-lab/server/client"
	"github.com/openbkn-ai/adp/execution-factory/capabilities-lab/server/model"
)

func (s *Service) resolveOrchestrationForTool(
	ctx context.Context,
	businessDomain, boxID string,
	tool client.ToolInfo,
) *model.Orchestration {
	if lineage, ok := s.resolveOperatorLineage(ctx, businessDomain, boxID, tool); ok {
		return lineage
	}

	return s.resolveOrchestrationByName(ctx, businessDomain, tool.Name)
}

func (s *Service) resolveOperatorLineage(
	ctx context.Context,
	businessDomain, boxID string,
	tool client.ToolInfo,
) (*model.Orchestration, bool) {
	if tool.SourceType == "operator" && tool.SourceID != "" {
		return &model.Orchestration{
			Enabled:      true,
			OperatorID:   tool.SourceID,
			OperatorName: tool.Name,
		}, true
	}

	if tool.ResourceObject == "operator" && boxID != "" && tool.ToolID != "" {
		lineage, err := s.Client.GetToolSourceLineage(
			ctx,
			businessDomain,
			boxID,
			tool.ToolID,
			s.DefaultUserID,
		)
		if err == nil && lineage.SourceType == "operator" && lineage.SourceID != "" {
			return &model.Orchestration{
				Enabled:      true,
				OperatorID:   lineage.SourceID,
				OperatorName: tool.Name,
			}, true
		}
	}

	return nil, false
}

func (s *Service) resolveOrchestrationByName(
	ctx context.Context,
	businessDomain, toolName string,
) *model.Orchestration {
	if toolName == "" {
		return nil
	}

	resp, err := s.Client.ListOperatorsByName(ctx, businessDomain, toolName)
	if err != nil || len(resp.Data) == 0 {
		return nil
	}

	for _, op := range resp.Data {
		if !isActiveOrchestrationOperatorStatus(op.Status) {
			continue
		}
		if strings.EqualFold(op.Name, toolName) && op.OperatorID != "" {
			return &model.Orchestration{
				Enabled:      true,
				OperatorID:   op.OperatorID,
				OperatorName: op.Name,
			}
		}
	}

	if resp.Data[0].OperatorID != "" && isActiveOrchestrationOperatorStatus(resp.Data[0].Status) {
		return &model.Orchestration{
			Enabled:      true,
			OperatorID:   resp.Data[0].OperatorID,
			OperatorName: resp.Data[0].Name,
		}
	}

	return nil
}

func isActiveOrchestrationOperatorStatus(status string) bool {
	switch status {
	case "published", "editing":
		return true
	default:
		return false
	}
}

func (s *Service) orchestrationFromResourceObject(tool client.ToolInfo) *model.Orchestration {
	if tool.ResourceObject != "operator" {
		return nil
	}

	return &model.Orchestration{Enabled: true, OperatorName: tool.Name}
}
