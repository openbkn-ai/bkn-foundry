package operator

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
)

// UpdateStatus update operator status.
func (op *operatorHandle) OperatorStatusUpdate(c *gin.Context) {
	var err error
	req := &interfaces.OperatorStatusUpdateReq{
		StatusItems: []*interfaces.OperatorStatusItem{},
	}
	err = c.ShouldBindHeader(req)
	if err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}
	if err = utils.GetBindJSONRaw(c, &req.StatusItems); err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}

	for _, item := range req.StatusItems {
		err = validator.New().Struct(item)
		if err != nil {
			rest.ReplyError(c, err)
			return
		}
	}
	var userID string
	userID, err = op.getUserID(c, "")
	if err != nil {
		rest.ReplyError(c, err)
		return
	}

	err = op.OperatorManager.UpdateOperatorStatus(c.Request.Context(), req, userID)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, nil)
}
