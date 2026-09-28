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

type prepareViewExtension struct {
	request          *interfaces.ResourceRequest
	sourceMetadata   map[string]any
	schemaDefinition []*interfaces.Property
	decodeDefinition bool
}

func (*prepareViewExtension) ValidateRequest(context.Context, *interfaces.ResourceRequest) error {
	return nil
}

func (extension *prepareViewExtension) Prepare(_ context.Context, req *interfaces.ResourceRequest) (string, []*interfaces.Property, error) {
	extension.request = req
	if extension.sourceMetadata != nil {
		req.SourceMetadata = extension.sourceMetadata
	}
	if extension.decodeDefinition {
		definition, err := interfaces.DecodeDerivedLogicDefinition(req.LogicDefinition)
		if err != nil {
			return "", nil, err
		}
		req.LogicDefinition = definition
	}
	if extension.schemaDefinition != nil {
		return interfaces.LogicType_Derived, extension.schemaDefinition, nil
	}
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

func TestResourceServiceUpdateLogicViewSourceMetadata(t *testing.T) {
	for _, tc := range []struct {
		name           string
		definition     map[string]any
		schema         []*interfaces.Property
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
			wantProperties: map[string]any{},
		},
		{
			name:           "clears scanned statistics when public schema changes",
			definition:     map[string]any{"source_resource_id": "source-1"},
			schema:         []*interfaces.Property{{Name: "alias", Type: interfaces.DataType_String}},
			wantProperties: map[string]any{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			extension := &prepareViewExtension{decodeDefinition: true, schemaDefinition: tc.schema, sourceMetadata: map[string]any{
				"properties":      map[string]any{},
				"source_resource": map[string]any{"original_name": "current.orders"},
			}}
			previous := GetLogicViewExtension()
			SetLogicViewExtension(extension)
			t.Cleanup(func() { SetLogicViewExtension(previous) })

			rs, mockRA, mockPS, _, _, mockCS, mockBTA := newTestService(t)
			expectResourceServiceTransaction(t, rs, true)
			mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			mockCS.EXPECT().CheckExistByID(gomock.Any(), "cat1").Return(true, nil)
			if tc.definition["source_resource_id"] != "source-1" || tc.schema != nil {
				mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil)
			}
			resource := &interfaces.Resource{
				ID: "r1", CatalogID: "cat1", Category: interfaces.ResourceCategoryLogicView,
				Name: "orders", LogicType: interfaces.LogicType_Derived,
				LogicDefinition:  map[string]any{"source_resource_id": "source-1"},
				SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String}},
				SourceMetadata: map[string]any{
					"properties":      map[string]any{"row_count": 6},
					"source_resource": map[string]any{"original_name": "old.orders"},
				},
			}
			mockRA.EXPECT().Update(gomock.Any(), gomock.Not(nil), gomock.Any(), int64(0)).
				DoAndReturn(func(_ context.Context, _ *sql.Tx, got *interfaces.Resource, _ int64) (int64, error) {
					assert.Equal(t, tc.wantProperties, got.SourceMetadata["properties"])
					assert.Equal(t, extension.sourceMetadata["source_resource"], got.SourceMetadata["source_resource"])
					return 1, nil
				})
			requestedSchema := tc.schema
			if requestedSchema == nil {
				requestedSchema = []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String}}
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
