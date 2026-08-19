package operator

import (
	"context"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

func checkIsDataSource(ctx context.Context, mode interfaces.ExecutionMode, isDataSourceReq *bool) (isDataSource bool, err error) {
	// Check whether it is a data source. If it is executed asynchronously, the data source is not supported.
	if isDataSourceReq == nil || !*isDataSourceReq {
		return
	}
	switch mode {
	case interfaces.ExecutionModeAsync:
		err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtOperatorAsyncDataSource,
			"async operator not support data source")
	case interfaces.ExecutionModeSync:
		isDataSource = *isDataSourceReq
	case interfaces.ExecutionModeStream:
		err = errors.DefaultHTTPError(ctx, http.StatusBadRequest, "stream operator not support data source")
	}
	return
}
