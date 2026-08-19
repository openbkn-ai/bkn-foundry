package operator

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
)

// Removal operation.
func (m *operatorManager) unpublishRelease(ctx context.Context, tx *sql.Tx, operator *model.OperatorRegisterDB, userID string) (err error) {
	exist, releaseDB, err := m.OpReleaseDB.SelectByOpID(ctx, operator.OperatorID)
	if err != nil {
		m.Logger.WithContext(ctx).Errorf("select operator release failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, "select operator release failed")
		return
	}
	if !exist {
		return
	}
	releaseDB.Status = interfaces.BizStatusOffline.String()
	// Delisting operation, set the current version history as delisted.
	has, historyDB, err := m.OpReleaseHistoryDB.SelectByOpIDAndMetdata(ctx, operator.OperatorID, operator.MetadataVersion)
	if err != nil {
		m.Logger.WithContext(ctx).Errorf("select operator history failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, "select operator history failed")
		return
	}
	if has { // add history.
		historyDB.OpRelease = utils.ObjectToJSON(releaseDB)
		err = m.OpReleaseHistoryDB.UpdateReleaseHistoryByID(ctx, tx, historyDB)
		if err != nil {
			m.Logger.WithContext(ctx).Errorf("update operator history failed, OperatorID: %s, err: %v", operator.OperatorID, err)
			err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, fmt.Sprintf("update operator history failed, OperatorID: %s, err: %s", operator.OperatorID, err.Error()))
		}
	} else {
		err = m.addReleaseHistory(ctx, tx, releaseDB, userID)
	}
	if err != nil {
		return
	}
	err = m.OpReleaseDB.UpdateByOpID(ctx, tx, releaseDB)
	if err != nil {
		m.Logger.WithContext(ctx).Errorf("update operator release failed, OperatorID: %s, err: %v", operator.OperatorID, err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, fmt.Sprintf("update operator release failed, OperatorID: %s, err: %s", operator.OperatorID, err.Error()))
	}
	return
}

// publishRelease publish operation.
func (m *operatorManager) publishRelease(ctx context.Context, tx *sql.Tx, operator *model.OperatorRegisterDB, userID string) (err error) {
	// Check if a released version exists.
	exist, releaseDB, err := m.OpReleaseDB.SelectByOpID(ctx, operator.OperatorID)
	if err != nil {
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, "select operator release failed")
		m.Logger.WithContext(ctx).Errorf("select operator release failed, OperatorID: %s, err: %v", operator.OperatorID, err)
		return
	}

	if exist { // If it exists, update the record and add the new release record to release_history.
		operatorRegisterToReleaseModel(operator, releaseDB)
		releaseDB.ReleaseUser = userID
		releaseDB.Tag++
		err = m.OpReleaseDB.UpdateByOpID(ctx, tx, releaseDB)
		if err != nil {
			m.Logger.WithContext(ctx).Errorf("update operator release failed, OperatorID: %s, err: %v", operator.OperatorID, err)
			err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, fmt.Sprintf("update operator release failed, OperatorID: %s, err: %s", operator.OperatorID, err.Error()))
			return
		}
	} else { // If it does not exist, add the record to release/release_history.
		releaseDB = &model.OperatorReleaseDB{}
		operatorRegisterToReleaseModel(operator, releaseDB)
		releaseDB.ReleaseUser = userID
		releaseDB.Tag++
		err = m.OpReleaseDB.Insert(ctx, tx, releaseDB)
		if err != nil {
			m.Logger.WithContext(ctx).Errorf("failed to create new release, OperatorID: %s, err: %v", operator.OperatorID, err)
			err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, fmt.Sprintf("create new release failed, OperatorID: %s, err: %s", operator.OperatorID, err.Error()))
			return
		}
	}
	err = m.addReleaseHistory(ctx, tx, releaseDB, userID)
	return
}

func (m *operatorManager) addReleaseHistory(ctx context.Context, tx *sql.Tx, releaseDB *model.OperatorReleaseDB, userID string) (err error) {
	now := time.Now().UnixNano()
	historyDB := &model.OperatorReleaseHistoryDB{
		OpID:            releaseDB.OpID,
		MetadataVersion: releaseDB.MetadataVersion,
		MetadataType:    releaseDB.MetadataType,
		OpRelease:       utils.ObjectToJSON(releaseDB),
		Tag:             releaseDB.Tag,
		CreateTime:      now,
		CreateUser:      userID,
		UpdateTime:      now,
		UpdateUser:      userID,
	}
	// Add records to release_history.
	err = m.OpReleaseHistoryDB.Insert(ctx, tx, historyDB)
	if err != nil {
		m.Logger.WithContext(ctx).Errorf("failed to insert release history record, OperatorID: %s, err: %v", releaseDB.OpID, err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, fmt.Sprintf("failed to insert release history record, OperatorID: %s, err: %s", releaseDB.OpID, err.Error()))
	}
	return
}

// operatorRegisterToReleaseModel registers configuration to release.
func operatorRegisterToReleaseModel(operator *model.OperatorRegisterDB, release *model.OperatorReleaseDB) {
	release.OpID = operator.OperatorID
	release.Name = operator.Name
	release.MetadataVersion = operator.MetadataVersion
	release.MetadataType = operator.MetadataType
	release.OperatorType = operator.OperatorType
	release.ExecutionMode = operator.ExecutionMode
	release.ExecuteControl = operator.ExecuteControl
	release.ExtendInfo = operator.ExtendInfo
	release.Source = operator.Source
	release.Category = operator.Category
	release.Status = operator.Status
	release.CreateUser = operator.CreateUser
	release.CreateTime = operator.CreateTime
	release.UpdateUser = operator.UpdateUser
	release.UpdateTime = operator.UpdateTime
	release.IsInternal = operator.IsInternal
	release.IsDataSource = operator.IsDataSource
}
