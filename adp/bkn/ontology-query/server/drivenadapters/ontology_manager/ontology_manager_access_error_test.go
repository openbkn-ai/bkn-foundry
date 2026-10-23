// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package ontology_manager

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	rmock "github.com/openbkn-ai/bkn-foundry/comm-go/rest/mock"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common"
	oerrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
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

func TestDownstreamErrorsUseOperationCodes(t *testing.T) {
	cases := []struct {
		name string
		code string
		post bool
		call func(context.Context, *ontologyManagerAccess) error
	}{
		{"object type", oerrors.OntologyQuery_ObjectType_InternalError_GetObjectTypesByIDFailed, false, func(ctx context.Context, a *ontologyManagerAccess) error {
			_, _, err := a.GetObjectType(ctx, "kn", "main", "ot")
			return err
		}},
		{"metric context", oerrors.OntologyQuery_Metric_InternalError_QueryFailed, false, func(ctx context.Context, a *ontologyManagerAccess) error {
			_, err := a.GetObjectMetricExecutionContext(ctx, "kn", "main", "metric", 1)
			return err
		}},
		{"relation paths", oerrors.OntologyQuery_KnowledgeNetwork_InternalError_GetRelationTypeFailed, true, func(ctx context.Context, a *ontologyManagerAccess) error {
			_, err := a.GetRelationTypePathsBaseOnSource(ctx, "kn", "main", interfaces.PathsQueryBaseOnSource{})
			return err
		}},
		{"relation type", oerrors.OntologyQuery_KnowledgeNetwork_InternalError_GetRelationTypeFailed, false, func(ctx context.Context, a *ontologyManagerAccess) error {
			_, _, err := a.GetRelationType(ctx, "kn", "main", "rt")
			return err
		}},
		{"relation list", oerrors.OntologyQuery_KnowledgeNetwork_InternalError_GetRelationTypeFailed, false, func(ctx context.Context, a *ontologyManagerAccess) error {
			_, err := a.ListRelationTypes(ctx, "kn", "main", interfaces.RelationTypesQuery{})
			return err
		}},
		{"action type", oerrors.OntologyQuery_ActionType_InternalError_GetActionTypesByIDFailed, false, func(ctx context.Context, a *ontologyManagerAccess) error {
			_, _, _, err := a.GetActionType(ctx, "kn", "main", "at")
			return err
		}},
		{"risk types", oerrors.OntologyQuery_RiskType_InternalError_GetRiskTypesByIDsFailed, false, func(ctx context.Context, a *ontologyManagerAccess) error {
			_, err := a.GetRiskTypesByIDs(ctx, "kn", "main", []string{"risk"})
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			client := rmock.NewMockHTTPClient(ctrl)
			access := newTestOntologyManagerAccess(&common.AppSetting{BKNBackendUrl: "http://test-om"}, client)
			source := rest.BaseError{ErrorCode: "BknBackend.Denied", Description: "downstream reason", ErrorDetails: "source detail", Metadata: map[string]any{"resource": "example"}}
			body, err := json.Marshal(source)
			if err != nil {
				t.Fatal(err)
			}
			if tc.post {
				client.EXPECT().PostNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(http.StatusForbidden, body, nil)
			} else {
				client.EXPECT().GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(http.StatusForbidden, body, nil)
			}
			err = tc.call(i18n.WithLanguage(t.Context(), i18n.AmericanEnglish), access)
			var got *rest.HTTPError
			if !errors.As(err, &got) {
				t.Fatalf("error=%T %v", err, err)
			}
			if got.HTTPCode != http.StatusForbidden || got.BaseError.ErrorCode != tc.code || got.Lang != i18n.AmericanEnglish {
				t.Fatalf("outer error=%#v", got)
			}
			if err := got.Render(); err != nil || got.BaseError.Description == "" {
				t.Fatalf("render=%v, %#v", err, got)
			}
			detail, ok := got.BaseError.ErrorDetails.(rest.BaseError)
			if !ok || detail.ErrorCode != source.ErrorCode || detail.Description != source.Description || detail.ErrorDetails != source.ErrorDetails || detail.Metadata["resource"] != "example" || len(got.BaseError.Metadata) != 0 {
				t.Fatalf("details=%#v", got.BaseError.ErrorDetails)
			}
		})
	}
}
