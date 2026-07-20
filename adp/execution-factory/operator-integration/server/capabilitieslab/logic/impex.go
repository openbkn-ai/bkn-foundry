// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/capabilitieslab/client"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/capabilitieslab/model"
)

type ImportCapabilityPackageResult struct {
	ComponentType string `json:"component_type"`
	Mode          string `json:"mode"`
}

func (s *Service) ExportCapability(
	ctx context.Context,
	businessDomain, capabilityID string,
) (json.RawMessage, string, error) {
	capability, err := s.GetCapability(ctx, businessDomain, capabilityID)
	if err != nil {
		return nil, "", err
	}

	if capability.Kind == "skill" {
		if capability.SkillID == "" {
			return nil, "", fmt.Errorf("skill capability missing id")
		}
		payload, filename, downloadErr := s.Client.DownloadSkillPackage(ctx, businessDomain, capability.SkillID)
		if downloadErr != nil {
			return nil, "", downloadErr
		}
		exportPayload, marshalErr := json.Marshal(map[string]interface{}{
			"skill_id":       capability.SkillID,
			"filename":       filename,
			"package_base64": base64.StdEncoding.EncodeToString(payload),
		})
		if marshalErr != nil {
			return nil, "", marshalErr
		}
		return exportPayload, "skill", nil
	}

	componentType, sourceID, err := impexTargetForCapability(capability)
	if err != nil {
		return nil, "", err
	}

	payload, exportErr := s.Client.ExportImpex(
		ctx,
		businessDomain,
		s.DefaultUserID,
		componentType,
		sourceID,
	)
	if exportErr != nil {
		return nil, "", exportErr
	}

	return payload, componentType, nil
}

func (s *Service) ImportCapabilityPackage(
	ctx context.Context,
	businessDomain, componentType, mode string,
	data []byte,
) (*ImportCapabilityPackageResult, error) {
	if len(data) == 0 {
		return nil, errors.New("import file is empty")
	}

	if componentType == "" {
		detected, detectErr := client.DetectImpexComponentType(data)
		if detectErr != nil {
			return nil, detectErr
		}
		componentType = detected
	}

	if mode == "" {
		mode = "create"
	}

	if err := s.Client.ImportImpex(ctx, businessDomain, s.DefaultUserID, componentType, mode, data); err != nil {
		return nil, err
	}

	return &ImportCapabilityPackageResult{
		ComponentType: componentType,
		Mode:          mode,
	}, nil
}

func impexTargetForCapability(capability *model.Capability) (componentType, sourceID string, err error) {
	switch capability.Kind {
	case "http":
		if capability.BoxID == "" {
			return "", "", fmt.Errorf("http capability has no group")
		}
		return "toolbox", capability.BoxID, nil
	case "function":
		if capability.BoxID == "" {
			return "", "", fmt.Errorf("function capability has no group")
		}
		return "toolbox", capability.BoxID, nil
	case "mcp":
		if capability.McpID == "" {
			return "", "", fmt.Errorf("mcp capability missing id")
		}
		return "mcp", capability.McpID, nil
	default:
		return "", "", fmt.Errorf("export not supported for %s capabilities", capability.Kind)
	}
}
