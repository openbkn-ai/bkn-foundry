// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package resource

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

type prepareViewService struct {
	request          *interfaces.ResourceRequest
	sourceMetadata   map[string]any
	schemaDefinition []*interfaces.Property
	decodeDefinition bool
}

func (*prepareViewService) ValidateRequest(context.Context, *interfaces.ResourceRequest) error {
	return nil
}

func (viewService *prepareViewService) Prepare(_ context.Context, req *interfaces.ResourceRequest) (string, []*interfaces.Property, error) {
	viewService.request = req
	if viewService.sourceMetadata != nil {
		req.SourceMetadata = viewService.sourceMetadata
	}
	if viewService.decodeDefinition {
		definition, err := interfaces.DecodeDerivedLogicDefinition(req.LogicDefinition)
		if err != nil {
			return "", nil, err
		}
		req.LogicDefinition = definition
	}
	if viewService.schemaDefinition != nil {
		return interfaces.LogicType_Derived, viewService.schemaDefinition, nil
	}
	return interfaces.LogicType_Derived, []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String}}, nil
}

func (*prepareViewService) QueryWithPaging(context.Context, *interfaces.Resource, *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
	return nil, errors.New("query is not expected")
}

func TestLogicViewServiceEntryPoints(t *testing.T) {
	previous := GetLogicViewService()
	t.Cleanup(func() { SetLogicViewService(previous) })
	ctx := context.Background()
	req := &interfaces.ResourceRequest{}
	view := &interfaces.Resource{}
	params := &interfaces.ResourceDataQueryParams{}

	SetLogicViewService(nil)
	for _, err := range []error{
		ValidateLogicViewRequest(ctx, req),
		func() error { _, _, err := PrepareLogicView(ctx, req); return err }(),
		func() error {
			result, err := QueryLogicViewWithPaging(ctx, view, params)
			assert.Nil(t, result)
			return err
		}(),
	} {
		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusNotImplemented, httpErr.HTTPCode)
	}

	service := &prepareViewService{}
	SetLogicViewService(service)
	require.NoError(t, ValidateLogicViewRequest(ctx, req))
	logicType, schema, err := PrepareLogicView(ctx, req)
	require.NoError(t, err)
	assert.Same(t, req, service.request)
	assert.Equal(t, interfaces.LogicType_Derived, logicType)
	assert.Equal(t, []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String}}, schema)
	result, err := QueryLogicViewWithPaging(ctx, view, params)
	assert.Nil(t, result)
	assert.EqualError(t, err, "query is not expected")
}

func TestResourceServiceUpdateLogicViewSourceMetadata(t *testing.T) {
	for _, tc := range []struct {
		name           string
		definition     map[string]any
		schema         []*interfaces.Property
		storedSchema   []*interfaces.Property
		requestSchema  []*interfaces.Property
		wantBuildCheck bool
		wantProperties map[string]any
	}{
		{
			name:           "preserves scanned statistics for metadata-only update",
			definition:     map[string]any{"source_resource_id": "source-1"},
			wantProperties: map[string]any{"row_count": 6},
		},
		{
			name:           "clears scanned statistics when definition changes",
			definition:     map[string]any{"source_resource_id": "source-2"},
			wantBuildCheck: true,
			wantProperties: map[string]any{},
		},
		{
			name:           "clears scanned statistics when public schema changes",
			definition:     map[string]any{"source_resource_id": "source-1"},
			schema:         []*interfaces.Property{{Name: "alias", Type: interfaces.DataType_String}},
			wantBuildCheck: true,
			wantProperties: map[string]any{},
		},
		{
			name:       "inherited features do not turn a name edit into a build change",
			definition: map[string]any{"source_resource_id": "source-1"},
			schema: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String,
				Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Keyword}}}},
			storedSchema: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String,
				Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Keyword}}}},
			requestSchema:  []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String}},
			wantProperties: map[string]any{"row_count": 6},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			viewService := &prepareViewService{decodeDefinition: true, schemaDefinition: tc.schema, sourceMetadata: map[string]any{
				"properties":      map[string]any{},
				"source_resource": map[string]any{"original_name": "current.orders"},
			}}
			previous := GetLogicViewService()
			SetLogicViewService(viewService)
			t.Cleanup(func() { SetLogicViewService(previous) })

			rs, mockRA, mockPS, _, _, mockCS, mockBTA := newTestService(t)
			expectResourceServiceTransaction(t, rs, true)
			mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			mockCS.EXPECT().CheckExistByID(gomock.Any(), "cat1").Return(true, nil)
			if tc.wantBuildCheck {
				mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil)
			}
			storedSchema := tc.storedSchema
			if storedSchema == nil {
				storedSchema = []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String}}
			}
			resource := &interfaces.Resource{
				ID: "r1", CatalogID: "cat1", Category: interfaces.ResourceCategoryLogicView,
				Name: "orders", LogicType: interfaces.LogicType_Derived,
				LogicDefinition:  map[string]any{"source_resource_id": "source-1"},
				SchemaDefinition: storedSchema,
				SourceMetadata: map[string]any{
					"properties":      map[string]any{"row_count": 6},
					"source_resource": map[string]any{"original_name": "old.orders"},
				},
			}
			mockRA.EXPECT().Update(gomock.Any(), gomock.Not(nil), gomock.Any(), int64(0)).
				DoAndReturn(func(_ context.Context, _ *sql.Tx, got *interfaces.Resource, _ int64) (int64, error) {
					assert.Equal(t, tc.wantProperties, got.SourceMetadata["properties"])
					assert.Equal(t, viewService.sourceMetadata["source_resource"], got.SourceMetadata["source_resource"])
					return 1, nil
				})
			requestedSchema := tc.schema
			if requestedSchema == nil {
				requestedSchema = []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String}}
			}
			if tc.requestSchema != nil {
				requestedSchema = tc.requestSchema
			}
			err := updateResourceForTest(t, rs, resource, &interfaces.ResourceRequest{
				CatalogID: "cat1", Category: interfaces.ResourceCategoryLogicView,
				Name: "renamed orders", LogicType: interfaces.LogicType_Derived,
				LogicDefinition:  tc.definition,
				SchemaDefinition: requestedSchema,
				SourceMetadata:   map[string]any{"properties": map[string]any{"row_count": 999}},
			})
			require.NoError(t, err)
		})
	}
}
