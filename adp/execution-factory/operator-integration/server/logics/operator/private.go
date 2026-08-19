package operator

import (
	"context"
	"fmt"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
)

// CheckAddAsTool checks whether the operator is allowed to be added as a tool.
func (m *operatorManager) CheckAddAsTool(ctx context.Context, operatorID, userID string) (resp *interfaces.CheckAddAsToolResp, err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	var accessor *interfaces.AuthAccessor
	accessor, err = m.AuthService.GetAccessor(ctx, userID)
	if err != nil {
		return
	}
	// Verify whether you have public access and use permissions for the operator.
	var authorized bool
	authorized, err = m.AuthService.OperationCheckAll(ctx, accessor, operatorID, interfaces.AuthResourceTypeOperator,
		interfaces.AuthOperationTypePublicAccess, interfaces.AuthOperationTypeExecute)
	if err != nil {
		return
	}
	if !authorized {
		err = errors.NewHTTPError(ctx, http.StatusForbidden, errors.ErrExtCommonUseForbidden, nil)
		return
	}
	// Query operator release information.
	exist, releaseDB, err := m.OpReleaseDB.SelectByOpID(ctx, operatorID)
	if err != nil {
		m.Logger.WithContext(ctx).Errorf("query operator release failed, err: %v", err)
		return nil, errors.DefaultHTTPError(ctx, http.StatusInternalServerError, "query operator release failed")
	}
	if !exist {
		return nil, errors.NewHTTPError(ctx, http.StatusNotFound, errors.ErrExtOperatorNotFound, "operator release not found")
	}
	// Check if the operator is available.
	if releaseDB.Status != interfaces.BizStatusPublished.String() {
		err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtOperatorNotAvailable,
			fmt.Sprintf("operator %s is not available", releaseDB.OpID), releaseDB.Name)
		return
	}
	// Check if execution is synchronous.
	if releaseDB.ExecutionMode != interfaces.ExecutionModeSync.String() {
		// Only supports synchronized operators converted to tools.
		err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtToolConvertOnlySupportSync,
			"only sync operators can be published as tools ")
		return
	}
	// Get metadata information.
	metadataDB, err := m.MetadataService.GetMetadataByVersion(ctx, interfaces.MetadataType(releaseDB.MetadataType), releaseDB.MetadataVersion)
	if err != nil {
		m.Logger.WithContext(ctx).Errorf("query metadata failed, err: %v", err)
		return nil, errors.DefaultHTTPError(ctx, http.StatusInternalServerError, "query metadata failed")
	}
	resp = &interfaces.CheckAddAsToolResp{
		OperatorID: releaseDB.OpID,
		Name:       releaseDB.Name,
		Metadata:   metadataDB,
	}
	return
}
