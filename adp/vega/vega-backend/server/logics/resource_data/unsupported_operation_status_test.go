// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package resource_data

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-comm-go/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	verrors "vega-backend/errors"
	"vega-backend/interfaces"
	mock_interfaces "vega-backend/interfaces/mock"
	"vega-backend/logics/filter_condition"
)

// 算子不支持是请求侧问题。这条链路上有两处会把它压回 500：QueryData 自己的兜底，
// 以及 query() 对 QueryData 返回值的重包——少改一处，映射就是死代码。
func TestQueryMapsUnsupportedOperationToBadRequest(t *testing.T) {
	newService := func(t *testing.T, connector interfaces.Connector) *resourceDataService {
		t.Helper()
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockCF := mock_interfaces.NewMockConnectorFactory(ctrl)
		mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", true).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true, ConnectorType: "mariadb"}, nil)
		mockCF.EXPECT().CreateConnectorInstance(gomock.Any(), "mariadb", gomock.Any()).Return(connector, nil)
		return &resourceDataService{cs: mockCS, cf: mockCF}
	}

	assertBadRequest := func(t *testing.T, err error) {
		t.Helper()
		require.Error(t, err)
		var httpErr *rest.HTTPError
		require.True(t, errors.As(err, &httpErr))
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Query_InvalidParameter, httpErr.BaseError.ErrorCode)
	}

	t.Run("表资源上算子不支持返回 400 而不是 500", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		tableConnector := mock_interfaces.NewMockTableConnector(ctrl)
		tableConnector.EXPECT().Connect(gomock.Any()).Return(nil)
		tableConnector.EXPECT().Close(gomock.Any()).Return(nil)
		tableConnector.EXPECT().ExecuteQuery(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(nil, filter_condition.NewUnsupportedOperationError("regex_extract", filter_condition.QueryChannelSQL))

		rds := newService(t, tableConnector)
		resource := &interfaces.Resource{
			ID: "resource-1", CatalogID: "catalog-1", Category: interfaces.ResourceCategoryTable,
			SchemaDefinition: []*interfaces.Property{{Name: "name"}},
		}

		_, _, err := rds.query(context.Background(), resource, &interfaces.ResourceDataQueryParams{})
		assertBadRequest(t, err)
	})

	t.Run("fileset 资源上算子不支持同样返回 400", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		filesetConnector := mock_interfaces.NewMockFilesetConnector(ctrl)
		filesetConnector.EXPECT().Connect(gomock.Any()).Return(nil)
		filesetConnector.EXPECT().Close(gomock.Any()).Return(nil)
		filesetConnector.EXPECT().ExecuteQuery(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(nil, filter_condition.NewUnsupportedOperationError(filter_condition.OperationMultiMatch,
				filter_condition.QueryChannelFileset))

		rds := newService(t, filesetConnector)
		resource := &interfaces.Resource{
			ID: "resource-2", CatalogID: "catalog-1", Category: interfaces.ResourceCategoryFileset,
			SchemaDefinition: []*interfaces.Property{{Name: "name"}},
		}

		_, _, err := rds.query(context.Background(), resource, &interfaces.ResourceDataQueryParams{})
		assertBadRequest(t, err)
	})

	t.Run("真正的执行失败仍是 500", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		tableConnector := mock_interfaces.NewMockTableConnector(ctrl)
		tableConnector.EXPECT().Connect(gomock.Any()).Return(nil)
		tableConnector.EXPECT().Close(gomock.Any()).Return(nil)
		tableConnector.EXPECT().ExecuteQuery(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(nil, errors.New("connection reset by peer"))

		rds := newService(t, tableConnector)
		resource := &interfaces.Resource{
			ID: "resource-3", CatalogID: "catalog-1", Category: interfaces.ResourceCategoryTable,
			SchemaDefinition: []*interfaces.Property{{Name: "name"}},
		}

		_, _, err := rds.query(context.Background(), resource, &interfaces.ResourceDataQueryParams{})
		require.Error(t, err)
		var httpErr *rest.HTTPError
		require.True(t, errors.As(err, &httpErr))
		assert.Equal(t, http.StatusInternalServerError, httpErr.HTTPCode)
	})
}
