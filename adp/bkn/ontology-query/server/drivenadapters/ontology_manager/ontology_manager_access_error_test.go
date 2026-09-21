// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package ontology_manager

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	rmock "github.com/openbkn-ai/bkn-foundry/comm-go/rest/mock"
	"go.uber.org/mock/gomock"

	"ontology-query/common"
	"ontology-query/interfaces"
)

func TestListRelationTypesPreservesForbiddenError(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := rmock.NewMockHTTPClient(ctrl)
	access := newTestOntologyManagerAccess(&common.AppSetting{BKNBackendUrl: "http://test-om"}, client)
	body, err := json.Marshal(rest.BaseError{ErrorCode: "Public.Forbidden", Description: "forbidden"})
	if err != nil {
		t.Fatal(err)
	}

	client.EXPECT().GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(http.StatusForbidden, body, nil)

	_, err = access.ListRelationTypes(context.Background(), "kn-1", "main", interfaces.RelationTypesQuery{})
	assertOntologyManagerHTTPStatus(t, err, http.StatusForbidden)
}

func TestGetRiskTypesByIDsPreservesForbiddenError(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := rmock.NewMockHTTPClient(ctrl)
	access := newTestOntologyManagerAccess(&common.AppSetting{BKNBackendUrl: "http://test-om"}, client)
	body, err := json.Marshal(rest.BaseError{ErrorCode: "Public.Forbidden", Description: "forbidden"})
	if err != nil {
		t.Fatal(err)
	}

	client.EXPECT().GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(http.StatusForbidden, body, nil)

	_, err = access.GetRiskTypesByIDs(context.Background(), "kn-1", "main", []string{"risk-1"})
	assertOntologyManagerHTTPStatus(t, err, http.StatusForbidden)
}
