// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/mocks"
)

// ontologyQueryServer answers every request with status and body, the way
// ontology-query reports a rejected request.
func ontologyQueryServer(t *testing.T, status int, body string) (*ontologyQueryClient, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	ctrl := gomock.NewController(t)
	logger := mocks.NewMockLogger(ctrl)
	logger.EXPECT().WithContext(gomock.Any()).Return(logger).AnyTimes()
	logger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
	logger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()
	return &ontologyQueryClient{
		logger:     logger,
		baseURL:    srv.URL + "/api/ontology-query",
		httpClient: rest.NewHTTPClientWithOptions(rest.HTTPClientOptions{TimeOut: 5}),
	}, srv.URL
}

// A rejected path is the caller's to fix, and what it needs is ontology-query's
// error code and details. They used to arrive buried in the shared HTTP client's
// transport string, "Exception(http do error, method: POST, url: http://<internal
// service>/..., error: {...})", which also handed the internal address to the model.
func TestQueryInstanceSubgraph_DownstreamPathErrorCarriesCodeAndDetails(t *testing.T) {
	const pathDetail = "第 2 条边 rel_order_user 与路径节点不一致：路径要求 user → order，当前填写 order → user。"
	client, internalURL := ontologyQueryServer(t, http.StatusBadRequest,
		`{"error_code":"OntologyQuery.KnowledgeNetwork.InvalidParameter.TypePath",`+
			`"description":"关系类路径参数无效","solution":"请检查参数是否正确。","error_link":"",`+
			`"error_details":"`+pathDetail+`"}`)

	_, err := client.QueryInstanceSubgraph(context.Background(), &interfaces.QueryInstanceSubgraphReq{
		KnID: "kn-1", RelationTypePaths: []any{},
	})

	he := asHTTPError(t, err)
	if he.HTTPCode != http.StatusBadRequest || he.Code != "Public.BadRequest" {
		t.Fatalf("error = %d %s, want 400 Public.BadRequest", he.HTTPCode, he.Code)
	}
	want := "OntologyQuery.KnowledgeNetwork.InvalidParameter.TypePath: " + pathDetail
	if he.ErrorDetails != want {
		t.Errorf("details = %#v, want %q", he.ErrorDetails, want)
	}
	if strings.Contains(he.Error(), internalURL) {
		t.Errorf("error exposes the internal service URL: %s", he.Error())
	}
}

// The downstream code alone still says which rule was broken when there are no details.
func TestClassifyQueryError_DownstreamCodeWithoutDetailsFallsBackToDescription(t *testing.T) {
	client, _ := ontologyQueryServer(t, http.StatusNotFound,
		`{"error_code":"OntologyQuery.KnowledgeNetwork.RelationTypeNotFound","description":"关系类不存在"}`)

	_, err := client.QueryInstanceSubgraph(context.Background(), &interfaces.QueryInstanceSubgraphReq{KnID: "kn-1"})

	he := asHTTPError(t, err)
	if he.HTTPCode != http.StatusNotFound {
		t.Fatalf("downstream 404 must stay a 404, got %d", he.HTTPCode)
	}
	if want := "OntologyQuery.KnowledgeNetwork.RelationTypeNotFound: 关系类不存在"; he.ErrorDetails != want {
		t.Errorf("details = %#v, want %q", he.ErrorDetails, want)
	}
}

// A 4xx whose body is not an ontology-query error envelope keeps the client's own
// detail rather than losing the cause.
func TestClassifyQueryError_NonEnvelopeBodyKeepsClientDetail(t *testing.T) {
	client, _ := ontologyQueryServer(t, http.StatusBadRequest, `upstream proxy said no`)

	_, err := client.QueryInstanceSubgraph(context.Background(), &interfaces.QueryInstanceSubgraphReq{KnID: "kn-1"})

	details, _ := asHTTPError(t, err).ErrorDetails.(string)
	if !strings.Contains(details, "upstream proxy said no") {
		t.Errorf("details = %q, want the downstream body", details)
	}
}

// A 5xx is a dependency fault and keeps the dependency-error classification.
func TestClassifyQueryError_DownstreamServerErrorIsUnchanged(t *testing.T) {
	client, _ := ontologyQueryServer(t, http.StatusInternalServerError,
		`{"error_code":"OntologyQuery.InternalError","description":"内部错误"}`)

	_, err := client.QueryInstanceSubgraph(context.Background(), &interfaces.QueryInstanceSubgraphReq{KnID: "kn-1"})

	he := asHTTPError(t, err)
	if he.HTTPCode != http.StatusInternalServerError || !strings.HasSuffix(he.Code, ".CommonExternalServerError") {
		t.Fatalf("error = %d %s, want 500 CommonExternalServerError", he.HTTPCode, he.Code)
	}
}
