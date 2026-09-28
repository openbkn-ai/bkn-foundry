// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package resource

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

type prepareViewExtension struct {
	request *interfaces.ResourceRequest
}

func (*prepareViewExtension) ValidateRequest(context.Context, *interfaces.ResourceRequest) error {
	return nil
}

func (extension *prepareViewExtension) Prepare(_ context.Context, req *interfaces.ResourceRequest) (string, []*interfaces.Property, error) {
	extension.request = req
	return interfaces.LogicType_Derived, []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String}}, nil
}

func (*prepareViewExtension) QueryWithPaging(context.Context, *interfaces.Resource, *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
	return nil, errors.New("query is not expected")
}

func TestResourceServicePrepareLogicView(t *testing.T) {
	extension := &prepareViewExtension{}
	previous := GetLogicViewExtension()
	SetLogicViewExtension(extension)
	t.Cleanup(func() { SetLogicViewExtension(previous) })

	req := &interfaces.ResourceRequest{Category: interfaces.ResourceCategoryLogicView}
	logicType, schema, err := (&resourceService{}).prepareLogicView(context.Background(), req)

	require.NoError(t, err)
	assert.Same(t, req, extension.request)
	assert.Equal(t, interfaces.LogicType_Derived, logicType)
	assert.Equal(t, []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String}}, schema)
}

func TestResourceServicePrepareLogicViewWithoutExtension(t *testing.T) {
	previous := GetLogicViewExtension()
	SetLogicViewExtension(nil)
	t.Cleanup(func() { SetLogicViewExtension(previous) })

	_, _, err := (&resourceService{}).prepareLogicView(context.Background(), &interfaces.ResourceRequest{})
	var httpErr *rest.HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusNotImplemented, httpErr.HTTPCode)
}
