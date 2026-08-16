// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"bkn-backend/interfaces"
)

const (
	RESOURCES_KEYWOED    = "keyword"
	RESOURCES_PAGE_LIMIT = "50"
)

// List metric model resources with pagination.
func (r *restHandler) ListResources(c *gin.Context) {
	logger.Debug("ListResources Start")

	// Read pagination parameters.
	resourceType := c.Query("resource_type")
	switch resourceType {
	case interfaces.RESOURCE_TYPE_KN:
		r.ListKnSrcs(c)
	default:
		// httpErr := rest.NewHTTPError(rest.GetLanguageCtx(c), http.StatusNotFound,
		// 	derrors.DataModel_MetricModel_MetricTaskNotFound)

		// // Set trace attributes for the error.
		// rest.ReplyError(c, httpErr)
	}

}
