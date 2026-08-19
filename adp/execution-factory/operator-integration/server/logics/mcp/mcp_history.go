package mcp

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
)

const (
	// MaxMCPHistoryRecords defines the maximum number of retained MCP server history records.
	MaxMCPHistoryRecords = 10
)

func (s *mcpServiceImpl) addMCPHistory(ctx context.Context, tx *sql.Tx, mcpRelease *model.MCPServerReleaseDB, userID string) error {
	now := time.Now().UnixNano()
	history := &model.MCPServerReleaseHistoryDB{
		CreateUser:  userID,
		CreateTime:  now,
		UpdateUser:  userID,
		UpdateTime:  now,
		MCPID:       mcpRelease.MCPID,
		MCPRelease:  utils.ObjectToJSON(mcpRelease),
		Version:     mcpRelease.Version,
		ReleaseDesc: mcpRelease.ReleaseDesc,
	}

	// Query existing history records.
	histories, err := s.DBMCPServerReleaseHistory.SelectByMCPID(ctx, tx, mcpRelease.MCPID)
	if err != nil {
		s.logger.WithContext(ctx).Warnf("failed to query existing MCP history records: %v", err)
		return fmt.Errorf("failed to query existing MCP history records: %w", err)
	}

	// If the number of historical records reaches the upper limit, delete excess records (maintain FIFO strategy)
	// Note: SelectByMCPID returns a descending order, with the latest record first and the oldest record last.
	if len(histories) >= MaxMCPHistoryRecords {
		// Calculate the number of records that need to be deleted and ensure that the upper limit is not exceeded after inserting new records.
		recordsToDelete := len(histories) - MaxMCPHistoryRecords + 1
		// Delete the oldest records starting from the end.
		startIndex := len(histories) - recordsToDelete
		for i := startIndex; i < len(histories); i++ {
			if err = s.DBMCPServerReleaseHistory.DeleteByID(ctx, tx, histories[i].ID); err != nil {
				s.logger.WithContext(ctx).Warnf("failed to delete old MCP history record: %v", err)
				return fmt.Errorf("failed to delete old MCP history record: %w", err)
			}

			// Remove MCP tool configuration information.
			mcpReleaseHistory := utils.JSONToObject[model.MCPServerReleaseDB](histories[i].MCPRelease)
			if mcpReleaseHistory.CreationType == interfaces.MCPCreationTypeToolImported.String() {
				err = s.removeMCPTools(ctx, tx, mcpReleaseHistory.MCPID, mcpReleaseHistory.Version)
				if err != nil {
					s.logger.WithContext(ctx).Warnf("failed to remove MCP tools: %v", err)
					return fmt.Errorf("failed to remove MCP tools: %w", err)
				}
			}
		}
	}

	var lastMCPReleaseHistory *model.MCPServerReleaseHistoryDB
	if len(histories) > 0 {
		lastMCPReleaseHistory = histories[0]
	}

	// The most recent one deleted is a historical MCP Server instance.
	if lastMCPReleaseHistory != nil {
		lastMCPRelease := utils.JSONToObject[model.MCPServerReleaseDB](lastMCPReleaseHistory.MCPRelease)
		if lastMCPRelease.CreationType == interfaces.MCPCreationTypeToolImported.String() {
			// Delete mcp Server instance.
			err = s.MCPInstanceService.DeleteMCPInstance(ctx, lastMCPRelease.MCPID, lastMCPRelease.Version)
			if err != nil {
				s.logger.WithContext(ctx).Warnf("failed to remove MCP server instance: %v", err)
				return fmt.Errorf("failed to remove MCP server instance: %w", err)
			}
		}
	}

	if lastMCPReleaseHistory != nil {
		if lastMCPReleaseHistory.Version > mcpRelease.Version {
			return nil
		}
		if lastMCPReleaseHistory.Version == mcpRelease.Version {
			// Delete current version release history.
			err = s.DBMCPServerReleaseHistory.DeleteByMCPIDAndVersion(ctx, tx, history.MCPID, history.Version)
			if err != nil {
				return err
			}
		}
	}
	// Insert new history record.
	if _, err := s.DBMCPServerReleaseHistory.Insert(ctx, tx, history); err != nil {
		s.logger.WithContext(ctx).Warnf("failed to insert new MCP history record: %v", err)
		return fmt.Errorf("failed to insert new MCP history record: %w", err)
	}
	return nil
}
