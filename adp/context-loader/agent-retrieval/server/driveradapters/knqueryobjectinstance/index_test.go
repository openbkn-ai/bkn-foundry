// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knqueryobjectinstance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/mocks"
)

func TestQueryObjectInstancePaging(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		code int
	}{
		{name: "default single", body: `{"offset":3}`, code: http.StatusOK},
		{name: "explicit cursor", body: `{"paging_mode":"cursor","offset":3,"cursor":"token"}`, code: http.StatusOK},
		{name: "cursor requires mode", body: `{"cursor":"token"}`, code: http.StatusBadRequest},
		{name: "single rejects cursor", body: `{"paging_mode":"single","cursor":"token"}`, code: http.StatusBadRequest},
		{name: "unknown mode", body: `{"paging_mode":"unknown"}`, code: http.StatusBadRequest},
		{name: "empty explicit mode", body: `{"paging_mode":""}`, code: http.StatusBadRequest},
		{name: "legacy search after", body: `{"search_after":["raw-position"]}`, code: http.StatusOK},
		{name: "legacy search after with cursor", body: `{"paging_mode":"cursor","cursor":"token","search_after":null}`, code: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ontology := mocks.NewMockDrivenOntologyQuery(gomock.NewController(t))
			if tc.code == http.StatusOK {
				ontology.EXPECT().QueryObjectInstances(gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, query *interfaces.QueryObjectInstancesReq) (*interfaces.QueryObjectInstancesResp, error) {
						wire, err := json.Marshal(query)
						if err != nil {
							t.Fatal(err)
						}
						var sent, requested map[string]any
						if err := json.Unmarshal(wire, &sent); err != nil {
							t.Fatal(err)
						}
						if err := json.Unmarshal([]byte(tc.body), &requested); err != nil {
							t.Fatal(err)
						}
						if _, present := sent["search_after"]; present {
							t.Fatal("retired field reached the downstream request")
						}
						for _, field := range []string{"paging_mode", "cursor", "offset"} {
							if sent[field] != requested[field] {
								t.Errorf("%s changed: sent %v, requested %v", field, sent[field], requested[field])
							}
						}
						return &interfaces.QueryObjectInstancesResp{
							Data:     []any{},
							Warnings: []string{"sort was ignored"},
						}, nil
					})
			}
			handler := &knQueryObjectInstanceHandler{OntologyQuery: ontology}
			router := gin.New()
			router.POST("/kn/query_object_instance", handler.QueryObjectInstance)
			req := httptest.NewRequest(http.MethodPost, "/kn/query_object_instance?kn_id=kn1&ot_id=ot1", strings.NewReader(tc.body))
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)
			if resp.Code != tc.code {
				t.Fatalf("status %d, want %d, body %s", resp.Code, tc.code, resp.Body.String())
			}
			if tc.code == http.StatusOK {
				var wire struct {
					IsSorted *bool    `json:"is_sorted"`
					Warnings []string `json:"warnings"`
				}
				if err := json.Unmarshal(resp.Body.Bytes(), &wire); err != nil {
					t.Fatal(err)
				}
				if wire.IsSorted == nil || *wire.IsSorted || len(wire.Warnings) != 1 || wire.Warnings[0] != "sort was ignored" {
					t.Fatalf("unexpected sort metadata: %s", resp.Body.String())
				}
			}
		})
	}
}
