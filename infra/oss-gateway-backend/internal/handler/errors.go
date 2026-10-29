// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package handler

import (
	stderrors "errors"

	"oss-gateway/internal/service"
	serviceerrors "oss-gateway/pkg/errors"
	"oss-gateway/pkg/outboundtls"
	"oss-gateway/pkg/response"

	"github.com/gin-gonic/gin"
)

func writeValidationError(c *gin.Context, err error) bool {
	var tlsErr *outboundtls.Error
	if stderrors.As(err, &tlsErr) {
		c.JSON(tlsErr.HTTPStatus(), response.Response{Code: tlsErr.Code, Message: tlsErr.Message})
		return true
	}
	var validationErr *service.StorageValidationError
	if !stderrors.As(err, &validationErr) {
		return false
	}
	response.ErrorWithCode(c, serviceerrors.GetErrorByCode(validationErr.Code), validationErr.Params, "")
	return true
}
