package toolbox

import (
	"context"
	"fmt"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
)

// toolboxAuthorizationType maps the persisted kind of a toolbox to its policy domain.
func toolboxAuthorizationType(metadataType string) (interfaces.AuthResourceType, error) {
	return interfaces.ToolboxAuthResourceType(interfaces.MetadataType(metadataType))
}

// authorizationTypeForBox never derives policy type from request metadata.
func (s *ToolServiceImpl) authorizationTypeForBox(ctx context.Context, boxID string) (interfaces.AuthResourceType, error) {
	if s == nil || s.ToolBoxDB == nil {
		return "", errors.DefaultHTTPError(ctx, http.StatusInternalServerError, "toolbox database is not configured")
	}
	exists, box, err := s.ToolBoxDB.SelectToolBox(ctx, boxID)
	if err != nil {
		return "", errors.DefaultHTTPError(ctx, http.StatusInternalServerError, "load toolbox authorization type")
	}
	if !exists || box == nil {
		return "", errors.NewHTTPError(ctx, http.StatusNotFound, errors.ErrExtToolBoxNotFound, "toolbox not found")
	}
	resourceType, err := toolboxAuthorizationType(box.MetadataType)
	if err != nil {
		return "", errors.DefaultHTTPError(ctx, http.StatusBadRequest, err.Error())
	}
	return resourceType, nil
}

func validateToolBoxMembership(ctx context.Context, tool *model.ToolDB, boxID string) error {
	if tool == nil || tool.BoxID != boxID {
		detail := "tool not found"
		if tool != nil {
			detail = fmt.Sprintf("tool %s not found", tool.ToolID)
		}
		return errors.NewHTTPError(ctx, http.StatusNotFound, errors.ErrExtToolNotFound, detail)
	}
	return nil
}

func (s *ToolServiceImpl) checkBoxViewPermission(ctx context.Context, accessor *interfaces.AuthAccessor, boxID string) error {
	resourceType, err := s.authorizationTypeForBox(ctx, boxID)
	if err != nil {
		return err
	}
	return s.AuthService.CheckViewPermission(ctx, accessor, boxID, resourceType)
}

func (s *ToolServiceImpl) checkBoxPublicAccessPermission(ctx context.Context, accessor *interfaces.AuthAccessor, boxID string) error {
	resourceType, err := s.authorizationTypeForBox(ctx, boxID)
	if err != nil {
		return err
	}
	return s.AuthService.CheckPublicAccessPermission(ctx, accessor, boxID, resourceType)
}

func (s *ToolServiceImpl) checkBoxModifyPermission(ctx context.Context, accessor *interfaces.AuthAccessor, boxID string) error {
	resourceType, err := s.authorizationTypeForBox(ctx, boxID)
	if err != nil {
		return err
	}
	return s.AuthService.CheckModifyPermission(ctx, accessor, boxID, resourceType)
}

func (s *ToolServiceImpl) checkBoxDeletePermission(ctx context.Context, accessor *interfaces.AuthAccessor, boxID string) error {
	resourceType, err := s.authorizationTypeForBox(ctx, boxID)
	if err != nil {
		return err
	}
	return s.AuthService.CheckDeletePermission(ctx, accessor, boxID, resourceType)
}

func (s *ToolServiceImpl) checkBoxPublishPermission(ctx context.Context, accessor *interfaces.AuthAccessor, boxID string) error {
	resourceType, err := s.authorizationTypeForBox(ctx, boxID)
	if err != nil {
		return err
	}
	return s.AuthService.CheckPublishPermission(ctx, accessor, boxID, resourceType)
}

func (s *ToolServiceImpl) checkBoxUnpublishPermission(ctx context.Context, accessor *interfaces.AuthAccessor, boxID string) error {
	resourceType, err := s.authorizationTypeForBox(ctx, boxID)
	if err != nil {
		return err
	}
	return s.AuthService.CheckUnpublishPermission(ctx, accessor, boxID, resourceType)
}

func (s *ToolServiceImpl) checkBoxExecutePermission(ctx context.Context, accessor *interfaces.AuthAccessor, boxID string) error {
	resourceType, err := s.authorizationTypeForBox(ctx, boxID)
	if err != nil {
		return err
	}
	if proxy, ok := interfaces.ProxyExecutionContextFromContext(ctx); ok &&
		(proxy.TargetType != string(resourceType) || proxy.TargetID != boxID) {
		return errors.DefaultHTTPError(ctx, http.StatusForbidden, "proxy target type does not match toolbox")
	}
	return s.AuthService.CheckExecutePermission(ctx, accessor, boxID, resourceType)
}

func (s *ToolServiceImpl) checkBoxOperationAny(ctx context.Context, accessor *interfaces.AuthAccessor, boxID string, operations ...interfaces.AuthOperationType) (bool, error) {
	resourceType, err := s.authorizationTypeForBox(ctx, boxID)
	if err != nil {
		return false, err
	}
	return s.AuthService.OperationCheckAny(ctx, accessor, boxID, resourceType, operations...)
}

// authorizedBoxIDs matches every policy ID against the actual stored box kind.
// A wildcard for one kind must never make boxes of the other kind visible.
func (s *ToolServiceImpl) authorizedBoxIDs(ctx context.Context, accessor *interfaces.AuthAccessor,
	metadataType interfaces.MetadataType, operations ...interfaces.AuthOperationType) ([]string, error) {
	kinds := []interfaces.MetadataType{interfaces.MetadataTypeAPI, interfaces.MetadataTypeFunc}
	if metadataType != "" {
		kinds = []interfaces.MetadataType{metadataType}
	}
	seen := map[string]bool{}
	result := make([]string, 0)
	for _, kind := range kinds {
		resourceType, err := toolboxAuthorizationType(string(kind))
		if err != nil {
			return nil, err
		}
		policyIDs, err := s.AuthService.ResourceListIDs(ctx, accessor, resourceType, operations...)
		if err != nil {
			return nil, err
		}
		if len(policyIDs) == 0 {
			continue
		}
		wildcard := false
		for _, id := range policyIDs {
			wildcard = wildcard || id == interfaces.ResourceIDAll
		}
		var boxes []*model.ToolboxDB
		if wildcard {
			boxes, err = s.ToolBoxDB.SelectToolBoxList(ctx,
				map[string]interface{}{"all": true, "metadata_type": string(kind)}, nil, nil)
		} else {
			boxes, err = s.ToolBoxDB.SelectListByBoxIDs(ctx, policyIDs)
		}
		if err != nil {
			return nil, err
		}
		actualIDs := make([]string, 0, len(boxes))
		for _, box := range boxes {
			if box.MetadataType == string(kind) {
				actualIDs = append(actualIDs, box.BoxID)
			}
		}
		for start := 0; start < len(actualIDs); start += interfaces.DefaultBatchSize {
			end := start + interfaces.DefaultBatchSize
			if end > len(actualIDs) {
				end = len(actualIDs)
			}
			allowed, err := s.AuthService.ResourceFilterIDs(ctx, accessor, actualIDs[start:end], resourceType, operations...)
			if err != nil {
				return nil, err
			}
			for _, id := range allowed {
				if !seen[id] {
					seen[id] = true
					result = append(result, id)
				}
			}
		}
	}
	return result, nil
}
