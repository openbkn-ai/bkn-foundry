// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package resource_data

import (
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	verrors "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	mock_interfaces "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
)

// bkn-foundry #3365：数据集能返回 JSON 字段，但它所走的本地索引查询 schema 不含该字段。
// 按它过滤原本报「not found in schema」，让 Agent 以为字段名写错；应报该字段不可过滤。
func TestQueryRefusesAConditionOnAFieldTheLocalIndexLeavesOut(t *testing.T) {
	for name, cfg := range map[string]*interfaces.FilterCondCfg{
		"equal": {Field: "claim_value", Operation: "==", Value: "EQ03", ValueFrom: interfaces.ValueFrom_Const},
		"match": {Field: "claim_value", Operation: "match", Value: "EQ03"},
		"nested": {Operation: "and", SubConds: []*interfaces.FilterCondCfg{
			{Field: "title", Operation: "==", Value: "a", ValueFrom: interfaces.ValueFrom_Const},
			{Field: "claim_value", Operation: "exist"},
		}},
	} {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			catalogs := mock_interfaces.NewMockCatalogService(ctrl)
			resources := mock_interfaces.NewMockResourceService(ctrl)
			// 不设 ListDocuments 期望：读取不能到达索引。
			datasets := mock_interfaces.NewMockDatasetService(ctrl)
			resource := &interfaces.Resource{ID: "dataset", CatalogID: "catalog", Category: interfaces.ResourceCategoryDataset,
				Enabled: true, Status: interfaces.ResourceStatusActive, LocalIndexName: "dataset-index",
				LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable,
				SchemaDefinition: []*interfaces.Property{
					{Name: "title", Type: interfaces.DataType_String},
					{Name: "claim_value", Type: interfaces.DataType_Json, Filterable: new(false)},
				}}
			resources.EXPECT().CheckResourcePermission(gomock.Any(), resource.ID, interfaces.OPERATION_TYPE_QUERY_DATA).Return(nil)
			catalogs.EXPECT().InternalGetByID(gomock.Any(), resource.CatalogID, true).Return(&interfaces.Catalog{Enabled: true}, nil)
			params := &interfaces.ResourceDataQueryParams{FilterCondCfg: cfg}

			result, err := (&resourceDataService{rs: resources, cs: catalogs, ds: datasets,
				lim: newResourceDataLocalIndexManagerMock(ctrl)}).QueryWithPaging(t.Context(), resource, params)

			var httpErr *httperrors.HTTPError
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
			assert.Equal(t, verrors.VegaBackend_Resource_InvalidParameter, httpErr.ErrorCode)
			assert.Contains(t, httpErr.ErrorDetails, `"claim_value" is not filterable`)
			assert.Nil(t, result)
		})
	}
}

// 构造条件所用的查询 schema 不被扩充，只有校验能看到补回的字段。
func TestWithUnqueryableSourceFieldsLeavesTheQuerySchemaAlone(t *testing.T) {
	title := &interfaces.Property{Name: "title", Type: interfaces.DataType_String, Filterable: new(true)}
	fields := map[string]*interfaces.Property{"title": title}
	source := []*interfaces.Property{
		{Name: "title", Type: interfaces.DataType_String},
		{Name: "claim_value", Type: interfaces.DataType_Json},
		nil,
	}

	checked := withUnqueryableSourceFields(fields, source)

	assert.Len(t, fields, 1)
	assert.Same(t, title, checked["title"])
	require.NotNil(t, checked["claim_value"])
	assert.False(t, checked["claim_value"].EffectiveFilterable())
	same := withUnqueryableSourceFields(fields, source[:1])
	assert.Len(t, same, 1)
}
