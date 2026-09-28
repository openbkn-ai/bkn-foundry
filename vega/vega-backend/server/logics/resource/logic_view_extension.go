// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package resource

import (
	"context"
	"net/http"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

var logicViewExtension struct {
	sync.RWMutex
	service interfaces.LogicViewExtension
}

// SetLogicViewExtension registers optional logic-view behavior during assembly.
// Services resolve it when handling a request because workers may be built
// before the Enterprise binary completes registration.
func SetLogicViewExtension(service interfaces.LogicViewExtension) {
	logicViewExtension.Lock()
	defer logicViewExtension.Unlock()
	logicViewExtension.service = service
}

func GetLogicViewExtension() interfaces.LogicViewExtension {
	logicViewExtension.RLock()
	defer logicViewExtension.RUnlock()
	return logicViewExtension.service
}

func (rs *resourceService) prepareLogicView(ctx context.Context, req *interfaces.ResourceRequest) (string, []*interfaces.Property, error) {
	if extension := GetLogicViewExtension(); extension != nil {
		return extension.Prepare(ctx, req)
	}
	return "", nil, rest.NewHTTPError(ctx, http.StatusNotImplemented, rest.PublicError_NotImplemented).
		WithErrorDetails("logic views require the Enterprise extension")
}
