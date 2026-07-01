// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logic

import (
	"context"

	"github.com/openbkn-ai/adp/execution-factory/capabilities-lab/server/client"
	"github.com/openbkn-ai/adp/execution-factory/capabilities-lab/server/model"
)

func auditFromTool(tool client.ToolInfo) *model.Audit {
	return compactAudit(&model.Audit{
		CreateUser:  tool.CreateUser,
		CreateTime:  tool.CreateTime,
		UpdateUser:  tool.UpdateUser,
		UpdateTime:  tool.UpdateTime,
		ReleaseUser: tool.ReleaseUser,
		ReleaseTime: tool.ReleaseTime,
	})
}

func auditFromToolDetail(tool *client.ToolDetail) *model.Audit {
	if tool == nil {
		return nil
	}
	return compactAudit(&model.Audit{
		CreateUser:  tool.CreateUser,
		CreateTime:  tool.CreateTime,
		UpdateUser:  tool.UpdateUser,
		UpdateTime:  tool.UpdateTime,
		ReleaseUser: tool.ReleaseUser,
		ReleaseTime: tool.ReleaseTime,
	})
}

func auditFromMcp(item client.McpSummary) *model.Audit {
	return compactAudit(&model.Audit{
		CreateUser:  item.CreateUser,
		CreateTime:  item.CreateTime,
		UpdateUser:  item.UpdateUser,
		UpdateTime:  item.UpdateTime,
		ReleaseUser: item.ReleaseUser,
		ReleaseTime: item.ReleaseTime,
	})
}

func auditFromSkill(item client.SkillSummary) *model.Audit {
	return compactAudit(&model.Audit{
		CreateUser:  item.CreateUser,
		CreateTime:  item.CreateTime,
		UpdateUser:  item.UpdateUser,
		UpdateTime:  item.UpdateTime,
		ReleaseUser: item.ReleaseUser,
		ReleaseTime: item.ReleaseTime,
	})
}

func auditFromSkillDetail(item *client.SkillDetailResponse) *model.Audit {
	if item == nil {
		return nil
	}
	return compactAudit(&model.Audit{
		CreateUser:  item.CreateUser,
		CreateTime:  item.CreateTime,
		UpdateUser:  item.UpdateUser,
		UpdateTime:  item.UpdateTime,
		ReleaseUser: item.ReleaseUser,
		ReleaseTime: item.ReleaseTime,
	})
}

func auditFromOperator(item *client.OperatorDetailResponse) *model.Audit {
	if item == nil {
		return nil
	}
	return compactAudit(&model.Audit{
		CreateUser:  item.CreateUser,
		CreateTime:  item.CreateTime,
		UpdateUser:  item.UpdateUser,
		UpdateTime:  item.UpdateTime,
		ReleaseUser: item.ReleaseUser,
		ReleaseTime: item.ReleaseTime,
	})
}

func compactAudit(audit *model.Audit) *model.Audit {
	if audit == nil {
		return nil
	}
	if audit.CreateUser == "" &&
		audit.CreateTime == 0 &&
		audit.UpdateUser == "" &&
		audit.UpdateTime == 0 &&
		audit.ReleaseUser == "" &&
		audit.ReleaseTime == 0 {
		return nil
	}
	return audit
}

func (s *Service) enrichOrchestrationAudit(ctx context.Context, businessDomain string, orchestration *model.Orchestration) {
	if orchestration == nil || orchestration.OperatorID == "" {
		return
	}
	operator, err := s.Client.GetOperator(ctx, businessDomain, orchestration.OperatorID)
	if err != nil {
		return
	}
	orchestration.Audit = auditFromOperator(operator)
}
