// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	resourcelogic "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/resource"
)

type validatingViewExtension struct{ err error }

func (extension *validatingViewExtension) ValidateRequest(context.Context, *interfaces.ResourceRequest) error {
	return extension.err
}

func (*validatingViewExtension) Prepare(context.Context, *interfaces.ResourceRequest) (string, []*interfaces.Property, error) {
	return "", nil, nil
}

func (*validatingViewExtension) QueryWithPaging(context.Context, *interfaces.Resource, *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
	return nil, errors.New("query is not expected")
}

func TestValidateResourceRequestLogicViewExtension(t *testing.T) {
	previous := resourcelogic.GetLogicViewExtension()
	t.Cleanup(func() { resourcelogic.SetLogicViewExtension(previous) })
	req := &interfaces.ResourceRequest{Name: "view", Category: interfaces.ResourceCategoryLogicView}
	want := errors.New("invalid view")
	resourcelogic.SetLogicViewExtension(&validatingViewExtension{err: want})
	require.ErrorIs(t, ValidateResourceRequest(context.Background(), req), want)

	resourcelogic.SetLogicViewExtension(nil)
	var httpErr *rest.HTTPError
	require.ErrorAs(t, ValidateResourceRequest(context.Background(), req), &httpErr)
	require.Equal(t, http.StatusNotImplemented, httpErr.HTTPCode)
}

func TestValidateResourceRequestIgnoresExpectedUpdateTime(t *testing.T) {
	err := ValidateResourceRequest(context.Background(), &interfaces.ResourceRequest{
		Name:               "resource",
		Category:           interfaces.ResourceCategoryTable,
		ExpectedUpdateTime: -1,
	})
	require.NoError(t, err)
}
