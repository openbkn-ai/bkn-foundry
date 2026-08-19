package toolbox

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
)

func (s *ToolServiceImpl) deleteTools(ctx context.Context, tx *sql.Tx, boxID string, tools []*model.ToolDB) (err error) {
	var toolIDs, apiMetadatas, funcMetadatas []string
	for _, tool := range tools {
		toolIDs = append(toolIDs, tool.ToolID)
		switch tool.SourceType {
		case model.SourceTypeOpenAPI:
			apiMetadatas = append(apiMetadatas, tool.SourceID)
		case model.SourceTypeOperator:
		case model.SourceTypeFunction:
			funcMetadatas = append(funcMetadatas, tool.SourceID)
		}
	}
	// Remove OpenAPI metadata.
	if len(apiMetadatas) > 0 {
		err = s.MetadataService.BatchDeleteMetadata(ctx, tx, interfaces.MetadataTypeAPI, apiMetadatas)
		if err != nil {
			s.Logger.WithContext(ctx).Errorf("delete metadata type API failed, err: %v", err)
			err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
			return
		}
	}
	// Delete Function metadata.
	if len(funcMetadatas) > 0 {
		err = s.MetadataService.BatchDeleteMetadata(ctx, tx, interfaces.MetadataTypeFunc, funcMetadatas)
		if err != nil {
			s.Logger.WithContext(ctx).Errorf("delete metadata type Function failed, err: %v", err)
			err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
			return
		}
	}
	// removal tool.
	if len(toolIDs) > 0 {
		err = s.ToolDB.DeleteBoxByIDAndTools(ctx, tx, boxID, toolIDs)
		if err != nil {
			s.Logger.WithContext(ctx).Errorf("delete tool failed, err: %v", err)
			err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
			return
		}
	}
	return
}

func (s *ToolServiceImpl) deleteToolBox(ctx context.Context, tx *sql.Tx, boxID string) (err error) {
	tools, err := s.ToolDB.SelectToolByBoxID(ctx, boxID)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("select tool failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	err = s.deleteTools(ctx, tx, boxID, tools)
	if err != nil {
		return
	}
	// Delete toolbox.
	err = s.ToolBoxDB.DeleteToolBox(ctx, tx, boxID)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("delete toolbox failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	// Delete configuration.
	err = s.IntCompConfigSvc.DeleteConfig(ctx, tx, interfaces.ComponentTypeToolBox.String(), boxID)
	return
}
